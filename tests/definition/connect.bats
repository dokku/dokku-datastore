#!/usr/bin/env bats
# connect runs the datastore's own client. bats has no terminal, which is how
# connect is run over ssh without -t: the client shows no prompt and reads
# statements from stdin, and each result has to come back as it runs rather
# than once stdin is closed, or a session looks like a blank screen.

load ../test_helper

# a statement each client runs, and a line of what it prints back
case "$PLUGIN" in
mysql | mariadb | postgres | clickhouse | omnisci)
  STATEMENT="SELECT 'streamed';"
  EXPECTED="streamed"
  ;;
redis)
  STATEMENT="ECHO streamed"
  EXPECTED="streamed"
  ;;
mongo)
  STATEMENT="print('streamed')"
  EXPECTED="streamed"
  ;;
memcached)
  STATEMENT="version"
  EXPECTED="VERSION"
  PIPED_SKIP="telnet exits once stdin is closed, before memcached answers"
  ;;
esac

setup_file() {
  datastore_setup_file
  if [[ -n "$STATEMENT" ]]; then
    create_service "$SERVICE"
  fi
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

skip_unless_statement() {
  if [[ -z "$STATEMENT" ]]; then
    skip "no statement to connect to $PLUGIN with"
  fi
}

connect_with_statement() {
  echo "$STATEMENT" | "$BIN" connect "$PLUGIN" "$SERVICE"
}

# writes one statement to connect through a pipe that is held open, and
# succeeds only if its result comes back before the pipe is closed
answers_while_stdin_is_open() {
  local fifo="$BATS_TEST_TMPDIR/stdin" out="$BATS_TEST_TMPDIR/stdout"
  local answered=1 pid stdin
  mkfifo "$fifo"

  # fd 3 is bats' own, and a background process holding it keeps bats waiting
  "$BIN" connect "$PLUGIN" "$SERVICE" <"$fifo" >"$out" 2>/dev/null 3>&- &
  pid=$!
  exec {stdin}>"$fifo"
  echo "$STATEMENT" >&"$stdin"

  for _ in $(seq 1 60); do
    if grep -q "$EXPECTED" "$out"; then
      answered=0
      break
    fi
    sleep 0.5
  done

  exec {stdin}>&-
  wait "$pid"
  return "$answered"
}

@test "($DEFINITION) connect runs statements piped to it" {
  skip_unless_statement
  if [[ -n "$PIPED_SKIP" ]]; then
    skip "$PIPED_SKIP"
  fi

  run --separate-stderr connect_with_statement
  assert_success
  assert_output --partial "$EXPECTED"
}

@test "($DEFINITION) connect answers a statement before stdin is closed" {
  skip_unless_statement
  if [[ "$PLUGIN" == "clickhouse" ]]; then
    skip "clickhouse client reads all of stdin before it runs anything"
  fi

  run answers_while_stdin_is_open
  assert_success
}
