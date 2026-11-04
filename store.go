package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store 三层文件哲学:
//   内存环  — 最近 24h,出图和检测直接读,进程内飞行记录器
//   原始层  — data/<target>/YYYY-MM-DD.jsonl,append-only,到期整文件删除
//   汇总层  — data/summary/<target>.jsonl,每天一行,永久保留
// 没有数据库。文本文件可以 grep、可以 tar、永远不会"坏得打不开"。

// ringSpan is how much history the in-memory ring holds, in seconds. It is a time
// window, not a round count: a count sized for 1/min silently shrinks the window for
// faster targets (pace=fast kept 6h, interval=5 kept 2h), which starved the 24h
// stats and the detector's 4h baseline. Memory per target is bounded by the
// interval: ~1.8MB for pace=fast (15s x 30 samples), ~20MB at interval=1.
const ringSpan = 24 * 60 * 60

type Store struct {
	mu    sync.RWMutex
	dir   string
	names []string          // 目标显示名(保序)
	dirs  map[string]string // name -> 目录名
	rings map[string][]Round
	total uint64 // 本进程累计轮数(心跳用)
	start time.Time
	cold  map[string]bool // day files Tier has already downsampled; only Tier touches it
}

func NewStore(dir string, targets []TargetCfg) (*Store, error) {
	s := &Store{dir: dir, dirs: map[string]string{}, rings: map[string][]Round{}, start: time.Now(),
		cold: map[string]bool{}}
	for _, t := range targets {
		s.names = append(s.names, t.Name)
		s.dirs[t.Name] = t.dir
		s.rings[t.Name] = nil
		if err := prepareRoutingDir(filepath.Join(dir, t.dir), t); err != nil {
			return nil, err
		}
	}
	return s, nil
}

const routingOwnerFile = ".routing-owner.json"

type routingOwner struct {
	Name          string `json:"name"`
	VRF           string `json:"vrf"`
	SourceAddress string `json:"source_address"`
}

func readRoutingOwner(dir string) (routingOwner, error) {
	var owner routingOwner
	path := filepath.Join(dir, routingOwnerFile)
	info, err := os.Lstat(path)
	if err != nil {
		return owner, err
	}
	if !info.Mode().IsRegular() {
		return owner, fmt.Errorf("non-regular routing owner: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return owner, err
	}
	err = json.Unmarshal(data, &owner)
	if err == nil && owner.Name == "" {
		err = fmt.Errorf("missing routing owner name: %s", path)
	}
	return owner, err
}

// Legacy automatic-routing directories remain readable without a marker. A
// generated directory can only be claimed while empty; never adopt old files.
func prepareRoutingDir(dir string, t TargetCfg) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe history directory: %s", dir)
	}
	want := routingOwner{t.Name, t.VRF, t.SourceAddress}
	owner, err := readRoutingOwner(dir)
	if err == nil {
		if owner != want {
			return fmt.Errorf("history directory belongs to another routing target: %s", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if t.VRF == "" && t.SourceAddress == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("unowned nonempty routing history directory: %s", dir)
	}
	data, err := json.Marshal(want)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".routing-owner-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// The store serializes publication; do not replace a marker found meanwhile.
	path := filepath.Join(dir, routingOwnerFile)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("routing owner appeared during initialization: %s", path)
	}
	return os.Rename(f.Name(), path)
}

func (s *Store) dayFile(name, day string) string {
	s.mu.RLock()
	dir := s.dirs[name]
	s.mu.RUnlock()
	return filepath.Join(s.dir, dir, day+".jsonl")
}

// Append 先落盘再进环:进程崩溃时宁可环里少一条,不可文件里丢一条。
func (s *Store) Append(name string, r Round) error {
	return s.appendContext(name, "", r)
}

// A stopped probe may finish after reload. Do not write its samples into the
// replacement context or resurrect a removed target.
func (s *Store) AppendTarget(t TargetCfg, r Round) error {
	return s.appendContext(t.Name, t.dir, r)
}

func (s *Store) appendContext(name, expectedDir string, r Round) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedDir != "" {
		if _, active := s.rings[name]; !active || s.dirs[name] != expectedDir {
			return nil
		}
	}
	day := time.Unix(r.T, 0).Format("2006-01-02")
	f, err := os.OpenFile(filepath.Join(s.dir, s.dirs[name], day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, ok := s.rings[name]; ok { // removed by a reload while this round was in flight
		s.rings[name] = ringAppend(s.rings[name], r)
	}
	s.total++
	return nil
}

// ringAppend adds r in time order and drops rounds older than ringSpan behind the
// newest. Order matters because Recent binary-searches the ring; the only way to
// arrive out of order is a probe loop stopped by a reload finishing its last round
// after the replacement loop has already written one.
//
// Dropping is a reslice, not a copy: append reallocates once the backing array is
// exhausted and copies only the live tail, so the dead head is reclaimed then. The
// dropped slots are cleared so their sample slices can be collected right away.
func ringAppend(ring []Round, r Round) []Round {
	n := len(ring)
	if n == 0 || ring[n-1].T <= r.T {
		ring = append(ring, r)
	} else {
		i := sort.Search(n, func(i int) bool { return ring[i].T > r.T })
		ring = append(ring, Round{})
		copy(ring[i+1:], ring[i:])
		ring[i] = r
	}
	cut := ring[len(ring)-1].T - ringSpan
	i := sort.Search(len(ring), func(i int) bool { return ring[i].T >= cut })
	if i > 0 {
		clear(ring[:i])
		ring = ring[i:]
	}
	return ring
}

// Replay 开机回放昨天+今天的文件,重建飞行记录器。
func (s *Store) Replay() {
	days := []string{
		time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
		time.Now().Format("2006-01-02"),
	}
	cut := time.Now().Unix() - ringSpan
	for _, name := range s.names {
		var ring []Round
		for _, day := range days {
			f, err := os.Open(s.dayFile(name, day))
			if err != nil {
				continue
			}
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				var r Round
				if json.Unmarshal(sc.Bytes(), &r) == nil && r.T >= cut {
					ring = append(ring, r)
				}
			}
			f.Close()
		}
		// day files are append-only and normally sorted; a reload overlap can leave
		// one round out of place, and Recent relies on order
		sort.SliceStable(ring, func(i, j int) bool { return ring[i].T < ring[j].T })
		s.mu.Lock()
		s.rings[name] = ring
		s.mu.Unlock()
		if len(ring) > 0 {
			log.Printf("[%s] replayed %d rounds", name, len(ring))
		}
	}
}

// Recent 返回 since 之后的轮(拷贝,调用方随便用)。
func (s *Store) Recent(name string, since int64) []Round {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ring := s.rings[name]
	i := sort.Search(len(ring), func(i int) bool { return ring[i].T >= since })
	out := make([]Round, len(ring)-i)
	copy(out, ring[i:])
	return out
}

func (s *Store) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.names...)
}

// EnsureTarget / RemoveTarget: storage side of hot reload. Removal only drops
// the in-memory ring; data files stay on disk.
func (s *Store) EnsureTarget(t TargetCfg) error {
	return s.EnsureTargets([]TargetCfg{t})
}

// EnsureTargets prepares every changed context before publishing any of them.
// A filesystem failure leaves all active rings/directories intact. Holding the
// lock also keeps in-flight old probes from appending across the context swap.
func (s *Store) EnsureTargets(targets []TargetCfg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prepared := make(map[string][]Round)
	for _, t := range targets {
		if err := prepareRoutingDir(filepath.Join(s.dir, t.dir), t); err != nil {
			return fmt.Errorf("[%s] init failed: %w", t.Name, err)
		}
		if _, exists := s.rings[t.Name]; exists && s.dirs[t.Name] == t.dir {
			continue
		}
		prepared[t.Name] = replayRoutingDir(filepath.Join(s.dir, t.dir))
	}
	for _, t := range targets {
		ring, changed := prepared[t.Name]
		if !changed {
			continue
		}
		if _, exists := s.rings[t.Name]; !exists {
			s.names = append(s.names, t.Name)
		}
		s.dirs[t.Name] = t.dir
		s.rings[t.Name] = ring
	}
	return nil
}

// Replay only this context, including when the user returns to automatic routing.
// Called while holding the store lock; another context cannot replace the ring.
func replayRoutingDir(dir string) []Round {
	now := time.Now()
	var rounds []Round
	for _, day := range []string{now.AddDate(0, 0, -1).Format("2006-01-02"), now.Format("2006-01-02")} {
		f, err := os.Open(filepath.Join(dir, day+".jsonl"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var r Round
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.T >= now.Unix()-ringSpan {
				rounds = append(rounds, r)
			}
		}
		f.Close()
	}
	sort.SliceStable(rounds, func(i, j int) bool { return rounds[i].T < rounds[j].T })
	return rounds
}

func (s *Store) RemoveTarget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rings, name)
	for i, n := range s.names {
		if n == name {
			s.names = append(s.names[:i], s.names[i+1:]...)
			break
		}
	}
}

// ReadRange reads raw rounds in [from,to]. ctx lets a long read stop early when the
// browser navigates away: without it, clicking through targets leaves every abandoned
// query still chewing through day files, and they starve each other.
func (s *Store) ReadRange(ctx context.Context, name string, from, to int64) []Round {
	var rounds []Round
	// ring first (covers the recent tail cheaply)
	s.mu.RLock()
	ring := append([]Round(nil), s.rings[name]...)
	dir := s.dirs[name]
	ringFrom := int64(1 << 62)
	if len(ring) > 0 {
		ringFrom = ring[0].T
	}
	s.mu.RUnlock()

	if from >= ringFrom {
		rounds = ring
	} else {
		// walk calendar days from local midnight: stepping from `from` itself skipped
		// the last day's file whenever to's time of day was earlier than from's
		y, m, dd := time.Unix(from, 0).Date()
		for d := time.Date(y, m, dd, 0, 0, 0, 0, time.Local); !d.After(time.Unix(to, 0)); d = d.AddDate(0, 0, 1) {
			select {
			case <-ctx.Done(): // client went away — stop reading, drop what we have
				return []Round{}
			default:
			}
			day, _ := readRoundFile(filepath.Join(s.dir, dir, d.Format("2006-01-02")+".jsonl"))
			rounds = append(rounds, day...)
		}
	}
	// clip to [from,to]. Always allocate: reusing rounds[:0] returns nil when rounds
	// is nil, which serializes as JSON null and blanks the chart instead of drawing
	// an empty window.
	out := make([]Round, 0, len(rounds))
	for _, r := range rounds {
		if r.T >= from && r.T <= to {
			out = append(out, r)
		}
	}
	return out
}

func (s *Store) readDay(name, day string) ([]Round, error) {
	return readRoundFile(s.dayFile(name, day))
}

func readRoundFile(path string) ([]Round, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	var out []Round
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Round
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Store) Counters() (uint64, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total, s.start
}

// Stats 对一组轮做分布统计。这里是"存分布不存均值"兑现的地方。
type Stats struct {
	Rounds  int     `json:"rounds"`
	P50     float64 `json:"p50"`
	P90     float64 `json:"p90"`
	P99     float64 `json:"p99"`
	LossPct float64 `json:"loss_pct"`
	Bursts  int     `json:"bursts"`
}

func calcStats(rounds []Round) Stats {
	st := Stats{Rounds: len(rounds)}
	var pool []float64
	sent, recv := 0, 0
	for _, r := range rounds {
		sent += r.S
		recv += r.R
		pool = append(pool, r.MS...)
		if r.B {
			st.Bursts++
		}
	}
	if sent > 0 {
		st.LossPct = 100 * float64(sent-recv) / float64(sent)
	}
	if len(pool) > 0 {
		sort.Float64s(pool)
		st.P50 = pct(pool, 50)
		st.P90 = pct(pool, 90)
		st.P99 = pct(pool, 99)
	}
	return st
}

// pct 要求已排序。
func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100)
	return sorted[idx]
}

// Retention:保留期就是 rm。按天分文件让清理不需要任何压缩整理逻辑。
// downsampleRound collapses a round's samples to [min, median, P90, max].
// The round itself is preserved — same timestamp, same sent/recv counts, same burst
// flag and z-score — so cold data flows through the exact same code path as hot data.
// Only the within-round redundancy is dropped: on a month-wide axis those 20-30
// samples land in the same pixel column anyway.
func downsampleRound(r Round) Round {
	if len(r.MS) <= 4 {
		return r // already small enough
	}
	sorted := append([]float64(nil), r.MS...)
	sort.Float64s(sorted)
	n := len(sorted)
	r.MS = []float64{
		round2(sorted[0]),
		round2(sorted[n/2]),
		round2(sorted[int(float64(n)*0.9)]),
		round2(sorted[n-1]),
	}
	return r
}

// Downsample rewrites one day file in place with per-round downsampling. Files are
// rewritten atomically via a temp file so a crash cannot leave a half-written day.
func (s *Store) Downsample(name, day string) error {
	return downsampleFile(s.dayFile(name, day))
}

func downsampleFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if _, ok := historyDayInfo(info); !ok {
		return fmt.Errorf("not a regular history day file: %s", path)
	}
	rounds, err := readRoundFile(path)
	if err != nil || len(rounds) == 0 {
		return err
	}
	// Already downsampled only if no round still has more than 4 samples. Checking
	// just the first round is wrong: a lossy first round (<=4 replies) made the whole
	// day look done, so days that began during an outage were never downsampled.
	if !slices.ContainsFunc(rounds, func(r Round) bool { return len(r.MS) > 4 }) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".downsample-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	for _, r := range rounds {
		line, err := json.Marshal(downsampleRound(r))
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		if _, err := f.Write(append(line, byte(10))); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Tier runs nightly: day files older than hotDays get downsampled in place, files
// older than keepDays are deleted. One pass, no separate cold directory — the
// tiering is invisible above the storage layer.
func (s *Store) Tier(hotDays, keepDays int) {
	hotCut := time.Now().AddDate(0, 0, -hotDays).Format("2006-01-02")
	keepCut := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	for _, dir := range s.historyDirs() {
		entries, err := os.ReadDir(filepath.Join(s.dir, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			fn := e.Name()
			day, ok := historyDay(e)
			if !ok {
				continue
			}
			path := filepath.Join(s.dir, dir, fn)
			switch {
			case day < keepCut:
				os.Remove(path)
				delete(s.cold, path)
				log.Printf("retention: removed %s/%s", dir, fn)
			case day < hotCut:
				// Cold files never change again, so each is read once per process
				// instead of every night: without this, every night re-read all ~270
				// cold days of every target just to find nothing to do.
				if s.cold[path] {
					continue
				}
				if err := downsampleFile(path); err != nil {
					log.Printf("downsample %s/%s failed: %v", dir, day, err)
					continue
				}
				s.cold[path] = true
			}
		}
	}
}

// Only persisted ownership, not a hash-shaped name, identifies archived contexts.
func (s *Store) historyDirs() []string {
	s.mu.RLock()
	names := make(map[string]bool, len(s.dirs))
	known := make(map[string]bool, len(s.dirs))
	for name, dir := range s.dirs {
		names[name] = true
		known[dir] = true
	}
	s.mu.RUnlock()
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		owner, err := readRoutingOwner(filepath.Join(s.dir, e.Name()))
		if err != nil || !names[owner.Name] || (owner.VRF == "" && owner.SourceAddress == "") {
			continue
		}
		h := sha256.Sum256([]byte(owner.VRF + "\x00" + owner.SourceAddress))
		want := dirSan.ReplaceAllString(owner.Name, "_") + fmt.Sprintf("--route-%x", h[:12])
		if e.Name() == want {
			known[e.Name()] = true
		}
	}
	var dirs []string
	for dir := range known {
		info, err := os.Lstat(filepath.Join(s.dir, dir))
		if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func historyDay(e os.DirEntry) (string, bool) {
	info, err := e.Info()
	if err != nil {
		return "", false
	}
	return historyDayInfo(info)
}

func historyDayInfo(info os.FileInfo) (string, bool) {
	name := info.Name()
	if !info.Mode().IsRegular() || len(name) != len("2006-01-02.jsonl") || !strings.HasSuffix(name, ".jsonl") {
		return "", false
	}
	day := name[:10]
	date, err := time.Parse("2006-01-02", day)
	return day, err == nil && date.Format("2006-01-02") == day
}

func (s *Store) Retention(days int) {
	if days <= 0 {
		return
	}
	cut := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	for _, dir := range s.historyDirs() {
		entries, err := os.ReadDir(filepath.Join(s.dir, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			day, ok := historyDay(e)
			if ok && day < cut {
				os.Remove(filepath.Join(s.dir, dir, e.Name()))
				log.Printf("retention: removed %s/%s", dir, e.Name())
			}
		}
	}
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func fmtDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
