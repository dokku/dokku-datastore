#!/usr/bin/env bash
# Compares two containers as docker sees them, normalised.
#
# The two backends are handed the same resolved values, so the containers they
# make should differ only where compose has to leave its own marks. This is the
# check that says so rather than assuming it, and the tool to reach for when a
# backend is suspected of drifting.
set -eo pipefail

LEFT="${1:?usage: $0 <container> <container>}"
RIGHT="${2:?usage: $0 <container> <container>}"

# the service name and its paths differ by construction, so they are replaced
# rather than compared; compose's own labels are dropped, since carrying them is
# the one difference that is allowed
normalise() {
  declare container="$1" name="$2"
  docker container inspect "$container" | jq -S --arg name "$name" '.[0] | {
    Cmd: .Config.Cmd,
    Entrypoint: .Config.Entrypoint,
    Env: (.Config.Env | sort),
    Image: .Config.Image,
    User: .Config.User,
    WorkingDir: .Config.WorkingDir,
    ExposedPorts: .Config.ExposedPorts,
    Hostname: (.Config.Hostname | sub($name; "SERVICE")),
    Labels: (.Config.Labels | with_entries(select(.key | startswith("com.docker.compose") | not))),
    RestartPolicy: .HostConfig.RestartPolicy,
    Memory: .HostConfig.Memory,
    ShmSize: .HostConfig.ShmSize,
    Privileged: .HostConfig.Privileged,
    ReadonlyRootfs: .HostConfig.ReadonlyRootfs,
    NetworkMode: .HostConfig.NetworkMode,
    CapAdd: .HostConfig.CapAdd,
    CapDrop: .HostConfig.CapDrop,
    # read write is the default either way: compose writes it out, the docker
    # cli leaves it implicit, and the mount is the same mount
    Binds: (.HostConfig.Binds // [] | map(sub($name; "SERVICE") | sub(":rw$"; "")) | sort)
  }'
}

left="$(mktemp)"
right="$(mktemp)"
trap 'rm -f "$left" "$right"' EXIT

normalise "$LEFT" "${LEFT##*.}" >"$left"
normalise "$RIGHT" "${RIGHT##*.}" >"$right"

if diff -u "$left" "$right"; then
  echo "==> $LEFT and $RIGHT are equivalent"
  exit 0
fi

echo "FAIL: $LEFT and $RIGHT differ outside com.docker.compose.*" >&2
exit 1
