#!/usr/bin/env bash
# Compares two containers as docker sees them, normalised.
#
# The two backends are handed the same resolved values, so the containers they
# make should differ only where compose has to leave its own marks. This is the
# check that says so rather than assuming it, and the tool to reach for when a
# backend is suspected of drifting.
set -eo pipefail

LEFT="${1:?usage: $0 <container> <container> [secret...]}"
RIGHT="${2:?usage: $0 <container> <container> [secret...]}"
shift 2
SECRETS=("$@")

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

# the service name reaches more than the paths: a datastore that authenticates
# on its command line carries it there too, so it is replaced everywhere rather
# than in the fields that happen to be known to hold it
scrub() {
  declare name="$1"
  sed "s/$name/SERVICE/g"
}

# a generated secret differs between two services by design, so comparing them
# would only ever report that they are two services. They are known values, so
# they are replaced rather than ignored.
redact() {
  declare -a filters=()
  for secret in "${SECRETS[@]}"; do
    [[ -n "$secret" ]] && filters+=(-e "s/$secret/REDACTED/g")
  done

  if [[ "${#filters[@]}" -eq 0 ]]; then
    cat -
    return
  fi

  sed "${filters[@]}"
}

normalise "$LEFT" "${LEFT##*.}" | scrub "${LEFT##*.}" | redact >"$left"
normalise "$RIGHT" "${RIGHT##*.}" | scrub "${RIGHT##*.}" | redact >"$right"

if diff -u "$left" "$right"; then
  echo "==> $LEFT and $RIGHT are equivalent"
  exit 0
fi

echo "FAIL: $LEFT and $RIGHT differ outside com.docker.compose.*" >&2
exit 1
