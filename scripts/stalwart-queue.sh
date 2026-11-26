#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

usage() {
  cat <<'EOF'
Usage:
  scripts/stalwart-queue.sh list
  scripts/stalwart-queue.sh purge [filters] [--apply]

Purge filters:
  --return-path-domain <domain>
  --older-than <duration>       Duration such as 30m, 12h, 7d, or 2w
  --dsn-only
  --all                         Requires --i-understand
  --i-understand                Required with --all, including dry runs
  --apply                       Delete matches; otherwise report a dry run

Credentials are read from STALWART_ADMIN_USERNAME and STALWART_ADMIN_PASSWORD.
STALWART_URL defaults to http://127.0.0.1:8080 in host-network mode,
otherwise http://127.0.0.1:8081.
EOF
}

fail() {
  printf 'ERROR: %s\n' "$1" >&2
  exit 1
}

duration_to_seconds() {
  local value="$1" amount unit multiplier
  if [[ ! "$value" =~ ^([1-9][0-9]{0,8})(s|m|h|d|w)$ ]]; then
    fail "invalid duration '$value' (use a positive integer followed by s, m, h, d, or w)"
  fi
  amount="${BASH_REMATCH[1]}"
  unit="${BASH_REMATCH[2]}"
  case "$unit" in
    s) multiplier=1 ;;
    m) multiplier=60 ;;
    h) multiplier=3600 ;;
    d) multiplier=86400 ;;
    w) multiplier=604800 ;;
  esac
  printf '%s\n' "$((amount * multiplier))"
}

[[ $# -ge 1 ]] || { usage >&2; exit 2; }
command_name="$1"
shift

filter_domain=""
filter_age_seconds=0
filter_dsn_only=false
apply_changes=false
all_messages=false
understand_all=false

case "$command_name" in
  list)
    [[ $# -eq 0 ]] || { usage >&2; exit 2; }
    ;;
  purge)
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --return-path-domain)
          [[ $# -ge 2 ]] || fail "--return-path-domain requires a domain"
          filter_domain="$2"
          [[ "$filter_domain" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$ ]] ||
            fail "invalid return-path domain"
          filter_domain="$(printf '%s' "$filter_domain" | tr '[:upper:]' '[:lower:]')"
          shift 2
          ;;
        --older-than)
          [[ $# -ge 2 ]] || fail "--older-than requires a duration"
          filter_age_seconds="$(duration_to_seconds "$2")"
          shift 2
          ;;
        --dsn-only)
          filter_dsn_only=true
          shift
          ;;
        --all)
          all_messages=true
          shift
          ;;
        --i-understand)
          understand_all=true
          shift
          ;;
        --apply)
          apply_changes=true
          shift
          ;;
        -h|--help)
          usage
          exit 0
          ;;
        *)
          printf 'ERROR: unknown purge option: %s\n' "$1" >&2
          usage >&2
          exit 2
          ;;
      esac
    done
    if [[ "$all_messages" == true ]]; then
      [[ "$understand_all" == true ]] || fail "--all requires --i-understand"
      [[ -z "$filter_domain" && "$filter_age_seconds" -eq 0 && "$filter_dsn_only" == false ]] ||
        fail "--all cannot be combined with other filters"
    else
      [[ "$understand_all" == false ]] || fail "--i-understand is only valid with --all"
      [[ -n "$filter_domain" || "$filter_age_seconds" -gt 0 || "$filter_dsn_only" == true ]] ||
        fail "purge requires a filter; use --all --i-understand to select the entire queue"
    fi
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    printf 'ERROR: unknown command: %s\n' "$command_name" >&2
    usage >&2
    exit 2
    ;;
esac

for tool in curl jq; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done

if [[ -z "${STALWART_URL:-}" ]]; then
  case "${STALWART_HOST_NETWORK:-false}" in
    true|1|yes) STALWART_URL="http://127.0.0.1:8080" ;;
    *) STALWART_URL="http://127.0.0.1:8081" ;;
  esac
fi
STALWART_URL="${STALWART_URL%/}"
if [[ ! "$STALWART_URL" =~ ^https?://(localhost|127\.0\.0\.1)(:([0-9]{1,5}))?$ ]]; then
  fail "STALWART_URL must be a loopback URL with no credentials or path; run this tool on the Stalwart host"
fi
if [[ -n "${BASH_REMATCH[3]:-}" ]]; then
  port_number=$((10#${BASH_REMATCH[3]}))
  ((port_number > 0 && port_number <= 65535)) || fail "STALWART_URL has an invalid port"
fi
: "${STALWART_ADMIN_USERNAME:?Set STALWART_ADMIN_USERNAME}"
: "${STALWART_ADMIN_PASSWORD:?Set STALWART_ADMIN_PASSWORD}"

umask 077
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/sendwise-stalwart-queue.XXXXXX")"
trap 'rm -rf -- "$tmp_dir"' EXIT
auth_file="$tmp_dir/auth.curl"
response_file="$tmp_dir/response.json"
request_file="$tmp_dir/request.json"
messages_file="$tmp_dir/messages.ndjson"
matches_file="$tmp_dir/matches.json"

curl_user="$(STALWART_CURL_USER="$STALWART_ADMIN_USERNAME:$STALWART_ADMIN_PASSWORD" \
  jq -nr 'env.STALWART_CURL_USER | @json')"
printf 'user = %s\n' "$curl_user" >"$auth_file"
chmod 600 "$auth_file"

JMAP_RESULT=""
PAGE_SIZE=""

jmap_call() {
  local method="$1" call_id="$2" args="$3" result
  jq -cn \
    --arg method "$method" \
    --arg call_id "$call_id" \
    --argjson args "$args" \
    '{
      using: ["urn:ietf:params:jmap:core", "urn:stalwart:jmap"],
      methodCalls: [[$method, $args, $call_id]]
    }' >"$request_file"

  if ! curl --fail --silent --show-error --max-time 20 \
    --config "$auth_file" \
    -H 'Content-Type: application/json' \
    --data-binary "@$request_file" \
    --output "$response_file" \
    "${STALWART_URL}/jmap/"; then
    printf 'ERROR: Stalwart JMAP request failed (%s)\n' "$method" >&2
    return 1
  fi

  if ! result="$(jq -ce \
    --arg method "$method" \
    --arg call_id "$call_id" '
      [.methodResponses[]? | select(.[2] == $call_id)] as $matches
      | if ($matches | length) != 1 then error("missing method response")
        elif $matches[0][0] == "error" then error("method returned error")
        elif $matches[0][0] != $method then error("unexpected method response")
        else $matches[0][1]
        end
    ' "$response_file" 2>/dev/null)"; then
    printf 'ERROR: Stalwart rejected or omitted %s\n' "$method" >&2
    return 1
  fi
  JMAP_RESULT="$result"
}

load_session() {
  if ! curl --fail --silent --show-error --max-time 20 \
    --config "$auth_file" \
    --header 'Accept: application/json' \
    --output "$response_file" \
    "${STALWART_URL}/jmap/session"; then
    fail "could not load the Stalwart JMAP session"
  fi
  PAGE_SIZE="$(jq -er '
    .capabilities["urn:ietf:params:jmap:core"].maxObjectsInGet
    | select(type == "number" and . > 0 and floor == .)
  ' "$response_file" 2>/dev/null)" ||
    fail "Stalwart JMAP session has no valid maxObjectsInGet"
}

fetch_snapshot() {
  local position=0 query_args query_ids id_count get_args page_total query_position expected_total=""
  local query_state expected_query_state=""
  : >"$messages_file"

  while true; do
    query_args="$(jq -cn \
      --argjson position "$position" \
      --argjson limit "$PAGE_SIZE" \
      '{position:$position, limit:$limit, calculateTotal:true}')"
    jmap_call "x:QueuedMessage/query" "queue-query-${position}" "$query_args" ||
      fail "could not read a complete Stalwart queue snapshot"

    page_total="$(jq -r 'if (.total | type) == "number" then .total else empty end' \
      <<<"$JMAP_RESULT")"
    [[ -n "$page_total" ]] || fail "Stalwart query did not report the queue total"
    [[ "$page_total" =~ ^[0-9]+$ ]] || fail "Stalwart returned an invalid queue total"
    query_position="$(jq -er '.position | select(type == "number" and . >= 0 and floor == .)' \
      <<<"$JMAP_RESULT" 2>/dev/null)" ||
      fail "Stalwart query did not report its position"
    [[ "$query_position" -eq "$position" ]] ||
      fail "Stalwart returned an unexpected queue position"
    query_state="$(jq -er '.queryState | select(type == "string" and length > 0)' \
      <<<"$JMAP_RESULT" 2>/dev/null)" ||
      fail "Stalwart query did not report queue state"
    if [[ -z "$expected_total" ]]; then
      expected_total="$page_total"
      expected_query_state="$query_state"
    elif [[ "$page_total" -ne "$expected_total" ]]; then
      fail "queue changed while reading it; no complete snapshot is available"
    elif [[ "$query_state" != "$expected_query_state" ]]; then
      fail "queue changed while reading it; no complete snapshot is available"
    fi

    query_ids="$(jq -ce '.ids | select(type == "array")' <<<"$JMAP_RESULT" 2>/dev/null)" ||
      fail "Stalwart returned an invalid queued-message query"
    id_count="$(jq -r 'length' <<<"$query_ids")"
    [[ "$id_count" -le "$PAGE_SIZE" ]] || fail "Stalwart query exceeded maxObjectsInGet"
    [[ "$id_count" -gt 0 ]] || break
    jq -e 'all(.[]; type == "string" and length > 0) and (length == (unique | length))' \
      <<<"$query_ids" >/dev/null ||
      fail "Stalwart returned invalid or duplicate queue ids"

    get_args="$(jq -cn --argjson ids "$query_ids" '{
      ids: $ids,
      properties: ["id", "createdAt", "returnPath", "recipients", "flags"]
    }')"
    jmap_call "x:QueuedMessage/get" "queue-get-${position}" "$get_args" ||
      fail "could not read a complete Stalwart queue snapshot"
    jq -e --argjson ids "$query_ids" '
      (.list | type) == "array"
      and ((.notFound // []) | length) == 0
      and (.list | length) == ($ids | length)
      and ([.list[] | .id] | sort) == ($ids | sort)
      and ([.list[] | .id] | unique | length) == ($ids | length)
    ' <<<"$JMAP_RESULT" >/dev/null ||
      fail "Stalwart returned an incomplete queued-message page"

    jq -c '.list[]' <<<"$JMAP_RESULT" >>"$messages_file"
    position=$((position + id_count))
    [[ "$id_count" -ge "$PAGE_SIZE" ]] || break
    if [[ -n "$expected_total" && "$position" -ge "$expected_total" ]]; then
      break
    fi
  done

  SNAPSHOT_COUNT="$(jq -s 'length' "$messages_file")"
  if [[ -n "$expected_total" && "$SNAPSHOT_COUNT" -ne "$expected_total" ]]; then
    fail "queue changed while reading it; no complete snapshot is available"
  fi
  jq -s -e '
    map(.id) as $ids
    | all($ids[]; type == "string" and length > 0)
      and (($ids | length) == ($ids | unique | length))
  ' "$messages_file" >/dev/null ||
    fail "Stalwart queue snapshot contained invalid or duplicate messages"
}

filter_snapshot() {
  local dsn_only="$filter_dsn_only"
  local now
  now="$(jq -n 'now | floor')"
  jq -s -c \
    --arg domain "$filter_domain" \
    --argjson older_seconds "$filter_age_seconds" \
    --argjson dsn_only "$dsn_only" \
    --argjson now "$now" '
      def is_dsn: ((.returnPath // "") == "<>" or (.flags.dsn // false) == true);
      def return_path_domain:
        (.returnPath // "" | gsub("^<|>$"; "")) as $return_path
        | if $return_path == "" then ""
          elif ($return_path | contains("@"))
          then ($return_path | split("@")[-1] | ascii_downcase)
          else ""
          end;
      [
        .[]
        | select(($dsn_only | not) or is_dsn)
        | select(($domain == "") or return_path_domain == $domain)
        | select(
            $older_seconds == 0
            or (
              (try (.createdAt | fromdateiso8601) catch null) as $created
              | $created != null and ($now - $created) >= $older_seconds
            )
          )
      ]
    ' "$messages_file" >"$matches_file"
}

print_list() {
  jq -s --argjson now "$(jq -n 'now | floor')" '
    def groups:
      sort | group_by(.) | map({name: .[0], count: length});
    def is_dsn:
      ((.returnPath // "") == "<>" or (.flags.dsn // false) == true);
    def return_path_domain:
      (.returnPath // "" | gsub("^<|>$"; "")) as $return_path
      | if $return_path == "" then "(DSN / none)"
        elif ($return_path | contains("@"))
        then ($return_path | split("@")[-1] | ascii_downcase)
          | if test("^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$")
            then . else "(unknown)" end
        else "(unknown)"
        end;
    def recipient_domain:
      gsub("^<|>$"; "")
      | split("@")
      | if length > 1 then .[-1] | ascii_downcase else "" end
      | if test("^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$")
        then . else "(unknown)" end;
    def recipient_status:
      if (.status | type) == "object" then (.status["@type"] // "(unknown)")
      elif (.status | type) == "string" then .status
      else "(unknown)"
      end
      | gsub("[^A-Za-z0-9_.-]"; "?");
    def age_bucket:
      (try (.createdAt | fromdateiso8601) catch null) as $created
      | if $created == null then "unknown"
        elif ($now - $created) < 3600 then "<1h"
        elif ($now - $created) < 86400 then "<24h"
        elif ($now - $created) < 604800 then "<7d"
        else "older"
        end;
    . as $messages
    | {
        total: ($messages | length),
        campaign: ([$messages[] | select(is_dsn | not)] | length),
        dsn: ([$messages[] | select(is_dsn)] | length),
        statuses: ([
          $messages[] | (.recipients // {}) | if type == "object" then .[] else empty end
          | recipient_status
        ] | groups),
        recipient_domains: ([
          $messages[] | (.recipients // {}) | if type == "object" then keys[] else empty end
          | recipient_domain
        ] | groups | sort_by(-.count, .name) | .[:10]),
        ages: ([$messages[] | age_bucket] | groups),
        return_path_domains: ([$messages[] | return_path_domain] | groups | sort_by(.name))
      }
  ' "$messages_file" |
    jq -r '
      . as $summary
      | "Total messages: \($summary.total)",
      "Campaign vs DSN:",
      "  Campaign: \($summary.campaign)",
      "  DSN: \($summary.dsn)",
      "Recipient status types:",
      (if ($summary.statuses | length) == 0 then "  (none)"
       else ($summary.statuses | map("  \(.name): \(.count)") | join("\n")) end),
      "Top recipient domains:",
      (if ($summary.recipient_domains | length) == 0 then "  (none)"
       else ($summary.recipient_domains | map("  \(.name): \(.count)") | join("\n")) end),
      "Message age:",
      (["<1h", "<24h", "<7d", "older", "unknown"] |
       map(. as $bucket |
         "  \($bucket): \(([$summary.ages[] | select(.name == $bucket) | .count][0] // 0))"
       ) | join("\n")),
      "Return-path domains:",
      (if ($summary.return_path_domains | length) == 0 then "  (none)"
       else ($summary.return_path_domains | map("  \(.name): \(.count)") | join("\n")) end)
    '
}

print_purge_preview() {
  local count
  count="$(jq 'length' "$matches_file")"
  printf 'Messages matching filters: %s\n' "$count"
  if [[ "$count" -eq 0 ]]; then
    printf 'No messages match; nothing to purge.\n'
    return
  fi
  printf 'Sample messages (up to 5; recipient local parts are hidden):\n'
  while IFS=$'\t' read -r sample_id recipient_count sample_domains; do
    printf '  id=%s recipients=%s sample=%s\n' "$sample_id" "$recipient_count" "$sample_domains"
  done < <(jq -r '
    def recipient_domain:
      gsub("^<|>$"; "")
      | split("@")
      | if length > 1 then .[-1] | ascii_downcase else "" end
      | if test("^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$")
        then . else "(unknown)" end;
    [
      .[:5][] as $message
      | ($message.recipients // {}) as $recipients
      | (if ($recipients | type) == "object"
         then [$recipients | keys[] | recipient_domain]
         else [] end | unique) as $domains
      | [
          ($message.id | tostring | gsub("[^A-Za-z0-9_-]"; "?")),
          ($recipients | if type == "object" then length else 0 end | tostring),
          ([$domains[:3][] | "*@" + .] | join(","))
        ]
      | @tsv
    ][]
  ' "$matches_file")
}

SNAPSHOT_COUNT=0
load_session
fetch_snapshot

if [[ "$command_name" == "list" ]]; then
  print_list
  exit 0
fi

filter_snapshot
print_purge_preview
if [[ "$apply_changes" == false ]]; then
  printf 'Dry run only. Add --apply to delete these messages.\n'
  exit 0
fi

match_count="$(jq 'length' "$matches_file")"
deleted_count=0
batch_offset=0
delete_failures=0
batch_number=0
if [[ "$match_count" -gt 0 ]]; then
  printf 'Deleting in batches of 200...\n'
  while [[ "$batch_offset" -lt "$match_count" ]]; do
    batch_ids="$(jq -c \
      --argjson start "$batch_offset" \
      '[.[$start:($start + 200)][] | .id]' "$matches_file")"
    batch_size="$(jq 'length' <<<"$batch_ids")"
    [[ "$batch_size" -gt 0 ]] || break
    batch_number=$((batch_number + 1))
    delete_args="$(jq -cn --argjson ids "$batch_ids" '{destroy:$ids}')"
    if ! jmap_call "x:QueuedMessage/set" "queue-delete-${batch_number}" "$delete_args"; then
      delete_failures=$((delete_failures + 1))
      printf 'Delete batch %s failed; stopping further deletions.\n' "$batch_number" >&2
      break
    fi

    if ! jq -e '
      type == "object"
      and ((.destroyed == null) or (.destroyed | type) == "array")
      and ((.notDestroyed == null) or (.notDestroyed | type) == "object")
    ' <<<"$JMAP_RESULT" >/dev/null; then
      delete_failures=$((delete_failures + 1))
      batch_offset=$((batch_offset + batch_size))
      printf 'Delete progress: %s/%s reported deleted\n' "$deleted_count" "$match_count"
      continue
    fi
    batch_deleted="$(jq '(.destroyed // []) | length' <<<"$JMAP_RESULT")"
    if ! jq -e --argjson ids "$batch_ids" '
      (.destroyed // []) as $destroyed
      | (.notDestroyed // {}) as $not_destroyed
      | (
          (($destroyed | length) == ($destroyed | unique | length))
          and all($destroyed[]; . as $id | ($ids | index($id)) != null)
          and all($ids[]; . as $id
            | (($destroyed | index($id)) != null)
              and (($not_destroyed | has($id)) | not))
        )
    ' <<<"$JMAP_RESULT" >/dev/null; then
      delete_failures=$((delete_failures + 1))
    fi
    deleted_count=$((deleted_count + batch_deleted))
    printf 'Delete progress: %s/%s reported deleted\n' "$deleted_count" "$match_count"
    if [[ "$batch_deleted" -ne "$batch_size" ]]; then
      delete_failures=$((delete_failures + 1))
    fi
    batch_offset=$((batch_offset + batch_size))
  done
fi

fetch_snapshot
filter_snapshot
remaining_count="$(jq 'length' "$matches_file")"
printf 'Remaining messages matching filters: %s\n' "$remaining_count"
if [[ "$remaining_count" -gt 0 || "$delete_failures" -gt 0 || "$deleted_count" -ne "$match_count" ]]; then
  fail "purge was incomplete; inspect the queue and retry with a narrower filter if appropriate"
fi
printf 'Purge verified.\n'
