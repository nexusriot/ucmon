#!/usr/bin/env bash
# Build the e2e image and run the suite in a sealed container, then rerun the
# signal tests with pidfd blocked. Extra arguments go to the test binary and
# skip the reruns, e.g.:
#
#   e2e/run.sh -test.run 'TUI'
#
set -euo pipefail
cd "$(dirname "$0")/.."

image=${UCMON_E2E_IMAGE:-ucmon-e2e:local}
DOCKER_BUILDKIT=1 docker build --file e2e/Dockerfile --tag "$image" .

# The sandbox is part of the test contract:
#  --init             a real PID 1 reaps orphans (an alert_cmd's children),
#                     which would otherwise pile up as zombies in the
#                     process list the tests read
#  --network none     hermetic: nothing reachable, not even a gateway to ping
#  --read-only        the image cannot be mutated by a test; /tmp is scratch
#  --cap-drop ALL     an unprivileged user, like ucmon on a real board
#  --memory           a cgroup limit with no swap, so a test can provoke a
#                     real OOM kill without touching the host's memory
#  /dev/kmsg          a FIFO from the host, not the host's kernel log: tests
#                     write kernel records into it, and ucmon never sees the
#                     real log (which an unprivileged user may not read anyway)
#  volumes            real kernel mounts that the fixture fstab describes:
#                     one locked read-only against fstab, one read-only by
#                     design, one read-write. Named per run and removed on
#                     exit: Docker 29+ refuses readonly anonymous volumes.
kmsgdir=$(mktemp -d)
vol=ucmon-e2e-$$
trap 'rm -rf "$kmsgdir"; docker volume rm -f "$vol-locked" "$vol-meant-ro" "$vol-data" >/dev/null 2>&1 || true' EXIT
mkfifo -m 666 "$kmsgdir/kmsg"
sandbox() {
	docker run --rm --init \
		--network none \
		--read-only --tmpfs /tmp:rw,exec,mode=1777 \
		--cap-drop ALL --security-opt no-new-privileges \
		--memory 512m --memory-swap 512m \
		--mount "type=volume,src=$vol-locked,dst=/mnt/locked,readonly" \
		--mount "type=volume,src=$vol-meant-ro,dst=/mnt/meant-ro,readonly" \
		--mount "type=volume,src=$vol-data,dst=/mnt/data" \
		--mount "type=bind,src=$kmsgdir/kmsg,dst=/dev/kmsg" \
		--env UCMON_E2E_SANDBOX=1 \
		"$@"
}

# With arguments, run just what was asked for, under the default profile.
if [ $# -gt 0 ]; then
	sandbox "$image" "$@"
	exit
fi

sandbox "$image"

# Docker's default seccomp profile allows pidfd_open, so the run above only
# takes the pidfd path. Rerun the signal tests where it is refused, as on
# kernels before 5.3 (ENOSYS) and under Docker < 20.10 or systemd's
# SystemCallFilter= (EPERM), to cover the check-then-kill fallback. These
# profiles allow everything else; caps are still dropped.
for mode in eperm enosys; do
	echo "=== pidfd blocked with $mode"
	sandbox \
		--security-opt "seccomp=e2e/testdata/seccomp-pidfd-$mode.json" \
		--env "UCMON_E2E_PIDFD=$mode" \
		"$image" -test.run 'Pidfd|TUIKills|TUIProcessDetail'
done
