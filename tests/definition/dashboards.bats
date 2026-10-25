#!/usr/bin/env bats
# A graphite service's grafana starts with the dashboards the definition
# provisions, in a folder of their own. The image ships dashboards as well, but
# nothing loads them, so before this a service had a datasource and nothing to
# look at it with.

load ../test_helper

setup_file() {
  datastore_setup_file
  [[ "$DEFINITION" == "graphite" ]] || return 0
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  if [[ "$DEFINITION" != "graphite" ]]; then
    skip "$DEFINITION does not run grafana"
  fi
}

# whether grafana serves both dashboards from the Dokku folder
provisioned() {
  local uid
  for uid in dokku-graphite dokku-statsd; do
    grafana "http://127.0.0.1:80/api/dashboards/uid/$uid" 2>/dev/null | grep -q '"folderTitle":"Dokku"' || return 1
  done
}

# provisioning runs as grafana starts, which can be after its health check
# answers
wait_for_dashboards() {
  wait_for_grafana || return 1
  for _ in $(seq 1 30); do
    provisioned && return 0
    sleep 2
  done

  echo "grafana did not provision the dashboards" >&2
  return 1
}

# the uid of the datasource the image adds once grafana is up, which differs on
# every service and is why the dashboards pick it with a variable
datasource_uid() {
  wait_for_datasource | grep -o '"uid":"[^"]*"' | head -n 1 | cut -d '"' -f 4
}

@test "($DEFINITION) grafana serves the provisioned dashboards" {
  run wait_for_dashboards
  assert_success

  run grafana "http://127.0.0.1:80/api/dashboards/uid/dokku-graphite"
  assert_success
  assert_output --partial '"provisioned":true'
  assert_output --partial '"folderTitle":"Dokku"'

  run grafana "http://127.0.0.1:80/api/dashboards/uid/dokku-statsd"
  assert_success
  assert_output --partial '"provisioned":true'
  assert_output --partial '"folderTitle":"Dokku"'
}

@test "($DEFINITION) a metric sent to statsd is found through the datasource the dashboards use" {
  local uid
  uid="$(datasource_uid)"
  [[ -n "$uid" ]] || fail "expected grafana to have the Local Graphite datasource"

  # statsd flushes every ten seconds, and the counter is sent again until it
  # does, since a udp packet sent before statsd listens is lost without a word.
  # The query is the one the StatsD dashboard fills its namespace picker with
  local found=""
  for _ in $(seq 1 30); do
    echo "dashboards.requests:1|c" |
      docker container run -i --rm --network "container:$(service_container)" busybox:1.37.0-uclibc nc -u -w 1 127.0.0.1 8125
    found="$(grafana "http://127.0.0.1:80/api/datasources/proxy/uid/$uid/metrics/find?query=stats.%7Bcounters,timers,gauges%7D.*" || true)"
    [[ "$found" == *'"text": "dashboards"'* ]] && break
    sleep 2
  done

  [[ "$found" == *'"text": "dashboards"'* ]] || fail "expected the dashboards namespace to be found, got $found"
}

@test "($DEFINITION) the dashboards are provisioned again when the service is rebuilt" {
  run rebuild_service
  assert_success

  run wait_for_dashboards
  assert_success
}
