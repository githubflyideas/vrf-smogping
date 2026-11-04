#!/bin/sh
# Run only in a disposable network namespace: see .github/workflows/test.yml.
set -eu
if [ "${JSON_PING_DISPOSABLE_NETNS:-}" != 1 ]; then
    printf '%s\n' 'Refusing network setup without JSON_PING_DISPOSABLE_NETNS=1' >&2
    exit 2
fi
binary=${1:?Pass an absolute path to the compiled Linux Go test binary}
blue=0
red=0
blue_dummy=0
red_dummy=0
cleanup() {
    [ "$red_dummy" = 0 ] || ip link del jp-dummy-red
    [ "$blue_dummy" = 0 ] || ip link del jp-dummy
    [ "$red" = 0 ] || ip link del jp-red
    [ "$blue" = 0 ] || ip link del jp-blue
}
trap cleanup EXIT
ip link set lo up
ip link add jp-blue type vrf table 10011
blue=1
ip link set jp-blue up
ip link add jp-dummy type dummy
blue_dummy=1
ip link set jp-dummy master jp-blue
ip addr add 192.0.2.2/32 dev jp-dummy
ip link set jp-dummy up
ip link add jp-red type vrf table 10012
red=1
ip link set jp-red up
ip link add jp-dummy-red type dummy
red_dummy=1
ip link set jp-dummy-red master jp-red
ip addr add 192.0.2.3/24 dev jp-dummy-red
ip link set jp-dummy-red up
JSON_PING_TEST_VRF=jp-blue JSON_PING_TEST_OTHER_VRF=jp-red \
JSON_PING_TEST_SOURCE=192.0.2.2 JSON_PING_TEST_OTHER_SOURCE=192.0.2.3 "$binary" -test.v -test.run=TestLinuxVRF
