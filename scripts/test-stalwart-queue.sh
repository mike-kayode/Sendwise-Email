#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for tool in jq; do
  command -v "$tool" >/dev/null 2>&1 || {
    printf 'ERROR: %s is required\n' "$tool" >&2
    exit 1
  }
done

test_dir="$(mktemp -d "${TMPDIR:-/tmp}/stalwart-queue-test.XXXXXX")"
trap 'rm -rf -- "$test_dir"' EXIT
bin_dir="$test_dir/bin"
mkdir -p "$bin_dir"
state_file="$test_dir/queue.json"
session_file="$test_dir/session.json"
calls_file="$test_dir/calls.log"
requests_file="$test_dir/requests.jsonl"

jq -n 'now as $now | [
  {
    id: "campaign-new",
    createdAt: (($now - 1800) | todateiso8601),
    returnPath: "bounce+new@bounces.example.test",
    recipients: {
      "alice@example.com": {status: {"@type": "Scheduled"}},
      "bob@sub.example.net": {status: {"@type": "Scheduled"}},
      "dave@example.org": {status: "Scheduled"}
    },
    flags: {}
  },
  {
    id: "campaign-old",
    createdAt: (($now - 172800) | todateiso8601),
    returnPath: "bounce+old@bounces.example.test",
    recipients: {
      "carol@example.com": {status: {"@type": "TemporaryFailure"}}
    },
    flags: {}
  },
  {
    id: "dsn-old",
    createdAt: (($now - 864000) | todateiso8601),
    returnPath: "<>",
    recipients: {
      "bounce@example.test": {status: {"@type": "TemporaryFailure"}}
    },
    flags: {dsn: true}
  }
]' >"$state_file"

jq -n '{
  capabilities: {
    "urn:ietf:params:jmap:core": {maxObjectsInGet: 2},
    "urn:stalwart:jmap": {}
  }
}' >"$session_file"

cat >"$bin_dir/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -euo pipefail
output=""
data_file=""
url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output|-o)
      output="$2"
      shift 2
      ;;
    --data-binary)
      data_file="${2#@}"
      shift 2
      ;;
    --config|-K|-H|--header|--max-time)
      shift 2
      ;;
    --fail|--silent|--show-error|-f|-s|-S)
      shift
      ;;
    *)
      if [[ "$1" == http://* || "$1" == https://* ]]; then
        url="$1"
      fi
      shift
      ;;
  esac
done

if [[ "$url" == */jmap/session ]]; then
  cp "$QUEUE_SESSION_FIXTURE" "$output"
  exit 0
fi

[[ -n "$data_file" && -f "$data_file" ]] || {
  printf 'fake curl: missing JMAP request\n' >&2
  exit 1
}
method="$(jq -r '.methodCalls[0][0]' "$data_file")"
call_id="$(jq -r '.methodCalls[0][2]' "$data_file")"
printf '%s\n' "$method" >>"$QUEUE_CURL_LOG"
jq -c '.methodCalls[0]' "$data_file" >>"$QUEUE_REQUESTS_LOG"

case "$method" in
  x:QueuedMessage/query)
    position="$(jq -r '.methodCalls[0][1].position' "$data_file")"
    limit="$(jq -r '.methodCalls[0][1].limit' "$data_file")"
    ids="$(jq -c --argjson position "$position" --argjson limit "$limit" \
      '[.[$position:($position + $limit)][] | .id]' "$QUEUE_STATE")"
    total="$(jq 'length' "$QUEUE_STATE")"
    query_state="fixture-state"
    if [[ "${QUEUE_FAKE_STATE_CHANGE:-false}" == true && "$position" -gt 0 ]]; then
      query_state="changed-state"
    fi
    result="$(jq -cn --argjson ids "$ids" --argjson total "$total" \
      --argjson position "$position" \
      --arg query_state "$query_state" \
      '{ids:$ids,total:$total,position:$position,queryState:$query_state}')"
    ;;
  x:QueuedMessage/get)
    ids="$(jq -c '.methodCalls[0][1].ids' "$data_file")"
    if [[ "${QUEUE_FAKE_INCOMPLETE_GET:-false}" == true ]]; then
      list='[]'
      not_found="$ids"
    else
      list="$(jq -c --argjson ids "$ids" \
        '[.[] | select(.id as $id | ($ids | index($id)) != null)]' "$QUEUE_STATE")"
      not_found='[]'
    fi
    result="$(jq -cn --argjson list "$list" --argjson not_found "$not_found" \
      '{list:$list,notFound:$not_found}')"
    ;;
  x:QueuedMessage/set)
    ids="$(jq -c '.methodCalls[0][1].destroy' "$data_file")"
    jq --argjson ids "$ids" \
      '[.[] | select(.id as $id | ($ids | index($id)) == null)]' \
      "$QUEUE_STATE" >"$QUEUE_STATE.next"
    mv "$QUEUE_STATE.next" "$QUEUE_STATE"
    result="$(jq -cn --argjson destroyed "$ids" \
      '{destroyed:$destroyed,notDestroyed:{}}')"
    ;;
  *)
    printf 'fake curl: unexpected JMAP method %s\n' "$method" >&2
    exit 1
    ;;
esac

jq -cn --arg method "$method" --arg call_id "$call_id" \
  --argjson result "$result" \
  '{methodResponses:[[$method,$result,$call_id]]}' >"$output"
FAKE_CURL
chmod +x "$bin_dir/curl"

run_tool() {
  PATH="$bin_dir:$PATH" \
    QUEUE_STATE="$state_file" \
    QUEUE_SESSION_FIXTURE="$session_file" \
    QUEUE_CURL_LOG="$calls_file" \
    QUEUE_REQUESTS_LOG="$requests_file" \
    QUEUE_FAKE_INCOMPLETE_GET="${QUEUE_FAKE_INCOMPLETE_GET:-false}" \
    QUEUE_FAKE_STATE_CHANGE="${QUEUE_FAKE_STATE_CHANGE:-false}" \
    STALWART_URL="${STALWART_URL:-http://127.0.0.1:8080}" \
    STALWART_ADMIN_USERNAME=test-operator \
    STALWART_ADMIN_PASSWORD=test-only-secret \
    "$ROOT/scripts/stalwart-queue.sh" "$@"
}

fail_test() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

list_output="$(run_tool list)"
grep -Fq 'Total messages: 3' <<<"$list_output" || fail_test "list total"
grep -Fq '  Campaign: 2' <<<"$list_output" || fail_test "campaign count"
grep -Fq '  DSN: 1' <<<"$list_output" || fail_test "DSN count"
grep -Fq '  Scheduled: 3' <<<"$list_output" || fail_test "Scheduled recipient count"
grep -Fq '  TemporaryFailure: 2' <<<"$list_output" || fail_test "TemporaryFailure recipient count"
grep -Fq '  example.com: 2' <<<"$list_output" || fail_test "recipient-domain count"
grep -Fq '  <1h: 1' <<<"$list_output" || fail_test "under-one-hour bucket"
grep -Fq '  <24h: 0' <<<"$list_output" || fail_test "under-one-day bucket"
grep -Fq '  <7d: 1' <<<"$list_output" || fail_test "under-seven-days bucket"
grep -Fq '  older: 1' <<<"$list_output" || fail_test "older bucket"
grep -Fq '  bounces.example.test: 2' <<<"$list_output" || fail_test "return-path domain grouping"
if grep -Fq 'alice@' <<<"$list_output"; then
  fail_test "list printed recipient local part"
fi
if STALWART_URL=http://127.0.0.1:8080@attacker.example run_tool list \
  >"$test_dir/non-loopback-url.log" 2>&1; then
  fail_test "accepted a URL with remote authority"
fi
grep -Fq 'must be a loopback URL' "$test_dir/non-loopback-url.log" ||
  fail_test "non-loopback URL refusal"

dry_output="$(run_tool purge --return-path-domain bounces.example.test)"
grep -Fq 'Messages matching filters: 2' <<<"$dry_output" || fail_test "domain dry-run count"
grep -Fq 'id=campaign-new' <<<"$dry_output" || fail_test "dry-run sample ids"
grep -Fq '*@example.com' <<<"$dry_output" || fail_test "masked recipient sample"
if grep -Fq 'alice@' <<<"$dry_output"; then
  fail_test "dry run printed recipient local part"
fi
if grep -Fq 'x:QueuedMessage/set' "$calls_file"; then
  fail_test "dry run attempted a deletion"
fi

age_output="$(run_tool purge --older-than 7d --dsn-only)"
grep -Fq 'Messages matching filters: 1' <<<"$age_output" || fail_test "age and DSN filters"

set_count_before="$(grep -c '^x:QueuedMessage/set$' "$calls_file" || true)"
if QUEUE_FAKE_INCOMPLETE_GET=true run_tool purge --all --i-understand --apply \
  >"$test_dir/incomplete-snapshot.log" 2>&1; then
  fail_test "purge proceeded with an incomplete queue snapshot"
fi
grep -Fq 'incomplete queued-message page' "$test_dir/incomplete-snapshot.log" ||
  fail_test "incomplete snapshot error"
set_count_after="$(grep -c '^x:QueuedMessage/set$' "$calls_file" || true)"
[[ "$set_count_after" -eq "$set_count_before" ]] ||
  fail_test "incomplete snapshot attempted a deletion"
if QUEUE_FAKE_STATE_CHANGE=true run_tool purge --all --i-understand --apply \
  >"$test_dir/state-change.log" 2>&1; then
  fail_test "purge proceeded after queue state changed during pagination"
fi
grep -Fq 'queue changed while reading it' "$test_dir/state-change.log" ||
  fail_test "queue state change error"
set_count_after="$(grep -c '^x:QueuedMessage/set$' "$calls_file" || true)"
[[ "$set_count_after" -eq "$set_count_before" ]] ||
  fail_test "queue state change attempted a deletion"

if run_tool purge --all >"$test_dir/all-without-confirmation.log" 2>&1; then
  fail_test "--all did not require --i-understand"
fi
grep -Fq -- '--all requires --i-understand' "$test_dir/all-without-confirmation.log" ||
  fail_test "--all refusal message"

apply_output="$(run_tool purge --return-path-domain bounces.example.test --apply)"
grep -Fq 'Remaining messages matching filters: 0' <<<"$apply_output" ||
  fail_test "post-delete verification"
[[ "$(jq 'length' "$state_file")" -eq 1 ]] || fail_test "domain purge removed wrong messages"

jq '.capabilities["urn:ietf:params:jmap:core"].maxObjectsInGet = 250' \
  "$session_file" >"$session_file.next"
mv "$session_file.next" "$session_file"
jq -n 'now as $now | [range(0; 205) as $index | {
  id: ("bulk-" + ($index | tostring)),
  createdAt: (($now - 1200) | todateiso8601),
  returnPath: "bulk@bulk.example.test",
  recipients: {("recipient-" + ($index | tostring) + "@bulk.example.test"):
    {status: {"@type": "Scheduled"}}},
  flags: {}
}]' >"$test_dir/bulk.json"
jq -s '.[0] + .[1]' "$state_file" "$test_dir/bulk.json" >"$state_file.next"
mv "$state_file.next" "$state_file"

bulk_output="$(run_tool purge --all --i-understand --apply)"
grep -Fq 'Messages matching filters: 206' <<<"$bulk_output" ||
  fail_test "all-queue confirmation apply count"
grep -Fq 'Remaining messages matching filters: 0' <<<"$bulk_output" ||
  fail_test "all-queue verification"
[[ "$(jq 'length' "$state_file")" -eq 0 ]] || fail_test "all purge left queued messages"
jq -s -e '
  map(select(.[0] == "x:QueuedMessage/set") | (.[1].destroy | length)) as $sizes
  | ($sizes | length) == 3
    and all($sizes[]; . <= 200)
    and any($sizes[]; . == 200)
    and any($sizes[]; . == 6)
    and any($sizes[]; . == 2)
' "$requests_file" >/dev/null || fail_test "delete batches did not split at 200"
jq -s -e '
  map(select(.[0] == "x:QueuedMessage/query")
    | {limit: .[1].limit, calculateTotal: .[1].calculateTotal}) as $queries
  | ($queries | length) > 0
    and all($queries[]; (.limit == 2 or .limit == 250) and .calculateTotal == true)
' "$requests_file" >/dev/null || fail_test "query ignored maxObjectsInGet or total"
jq -s -e '
  map(select(.[0] == "x:QueuedMessage/get") | (.[1].ids | length)) as $gets
  | ($gets | length) > 0 and all($gets[]; . <= 250)
' "$requests_file" >/dev/null || fail_test "get exceeded maxObjectsInGet"
if grep -Ev '^x:QueuedMessage/(query|get|set)$' "$calls_file"; then
  fail_test "unexpected JMAP method was called"
fi
if grep -Fq 'test-only-secret' <<<"$list_output$dry_output$age_output$apply_output$bulk_output"; then
  fail_test "password was printed"
fi

printf 'PASS: Stalwart queue list, filtering, dry-run, deletion, batching, and verification\n'
