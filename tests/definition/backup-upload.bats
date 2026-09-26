#!/usr/bin/env bats
# Ships a service's data to a local s3 server and puts it back from what landed
# there, so that a backup is known to hold the data rather than only to report
# that it finished.

load ../test_helper

# the backup image the binary pins, which also carries the aws cli used to read
# back what it uploaded
S3BACKUP_IMAGE="$(awk -F'"' '/S3BackupImage *=/ { print $2; exit }' "$REPO_ROOT/internal/hostenv/hostenv.go")"

# an s3 server that runs from a single container
S3_IMAGE="chrislusf/seaweedfs:4.47"
S3_CONTAINER="$SERVICE-s3"
S3_BUCKET="backups"
S3_ACCESS_KEY_ID="datastore"
S3_SECRET_ACCESS_KEY="datastore-secret"

# a docker cli to run the binary in, standing in for a dokku installed in docker
DOCKER_CLI_IMAGE="docker:29.8.1-cli"

# the backup container is run on the default bridge, so the server is reached
# by its address there rather than by name
s3_endpoint() {
  echo "http://$(container_inspect "$S3_CONTAINER" '{{ .NetworkSettings.Networks.bridge.IPAddress }}'):8333"
}

aws_cli() {
  docker container run --rm -i --entrypoint aws \
    -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID" \
    -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY" \
    -e AWS_DEFAULT_REGION=us-east-1 \
    "$S3BACKUP_IMAGE" --endpoint-url "$(s3_endpoint)" "$@"
}

# downloads the only backup in the bucket and unpacks it into the test's own
# directory, failing unless it holds a dump with something in it
download_backup() {
  local key
  key="$(aws_cli s3 ls "s3://$S3_BUCKET/" | awk '{ print $4 }')"
  [[ -n "$key" ]] || fail "no backup was uploaded"
  [[ "$(wc -l <<<"$key")" -eq 1 ]] || fail "expected one backup, got: $key"

  aws_cli s3 cp "s3://$S3_BUCKET/$key" - >"$BATS_TEST_TMPDIR/backup.tgz"
  mkdir -p "$BATS_TEST_TMPDIR/extracted"
  tar -xzf "$BATS_TEST_TMPDIR/backup.tgz" -C "$BATS_TEST_TMPDIR/extracted"
  [[ -s "$BATS_TEST_TMPDIR/extracted/backup/export" ]] || fail "the backup holds no dump"
}

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"

  docker container run -d --name "$S3_CONTAINER" \
    -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID" \
    -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY" \
    "$S3_IMAGE" server -s3 -dir=/data >/dev/null

  for _ in $(seq 1 60); do
    if aws_cli s3 mb "s3://$S3_BUCKET" >/dev/null 2>/dev/null; then
      break
    fi
    sleep 1
  done
  aws_cli s3 ls "s3://$S3_BUCKET" >/dev/null

  # a datastore without backups says so here, and each test skips on its own
  "$BIN" backup-auth "$PLUGIN" "$SERVICE" "$S3_ACCESS_KEY_ID" "$S3_SECRET_ACCESS_KEY" us-east-1 s3v4 "$(s3_endpoint)" ||
    [[ "$?" -eq "$NOT_IMPLEMENTED_EXIT" ]]
}

teardown_file() {
  docker container rm -f "$S3_CONTAINER" >/dev/null 2>/dev/null || true
  datastore_teardown_file "$SERVICE"
}

setup() {
  aws_cli s3 rm --recursive "s3://$S3_BUCKET/" >/dev/null
}

@test "($DEFINITION) backup ships the dump and it restores" {
  local probe="$REPO_ROOT/tests/probes/$DEFINITION.sh"
  if [[ -x "$probe" ]]; then
    run "$probe" write "$SERVICE"
    assert_success
  fi

  run "$BIN" backup "$PLUGIN" "$SERVICE" "$S3_BUCKET"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup"
  fi
  assert_success
  assert_output --partial "finished successfully"

  download_backup

  # overwritten first, so that finding the record afterwards means the backup
  # put it back rather than that it was never gone
  if [[ -x "$probe" ]]; then
    run "$probe" clobber "$SERVICE"
    assert_success
  fi

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$BATS_TEST_TMPDIR/extracted/backup/export" </dev/null
  assert_success

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi
}

# issue 18: with dokku installed in docker, the dump was written to a directory
# dockerd could not see, and an empty backup was shipped as a success. The
# binary is run in a container of its own here, with a /tmp of its own, talking
# to the host's dockerd the way a dokku installed in docker does
@test "($DEFINITION) backup ships the dump when dockerd cannot see this process's files" {
  command -v go >/dev/null || skip "go is needed to build a binary for the container"

  local bin="$BATS_FILE_TMPDIR/dokku-datastore-linux" arch
  arch="$(docker version --format '{{ .Server.Arch }}')"
  if [[ ! -x "$bin" ]]; then
    (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$bin" .)
  fi

  run docker container run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$bin:/usr/local/bin/dokku-datastore:ro" \
    -v "$DOKKU_LIB_ROOT:/var/lib/dokku" \
    -e DOKKU_LIB_ROOT=/var/lib/dokku \
    -e DOKKU_LIB_HOST_ROOT="$DOKKU_LIB_ROOT" \
    -e DOKKU_SYSTEM_USER=root \
    -e DOKKU_SYSTEM_GROUP=root \
    -e DOKKU_DATASTORE_BACKEND \
    "$DOCKER_CLI_IMAGE" dokku-datastore backup "$PLUGIN" "$SERVICE" "$S3_BUCKET"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup"
  fi
  assert_success
  assert_output --partial "finished successfully"

  download_backup
}
