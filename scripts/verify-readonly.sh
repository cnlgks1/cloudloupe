#!/usr/bin/env bash
#
# verify-readonly.sh — cloudloupe가 조회 계열 AWS API만 호출하도록 강제한다.
#
# cloudloupe는 AWS 리소스를 생성하거나 수정하거나 삭제할 수 없어야 한다. 이 스크립트가
# 그 보장의 기계적인 절반이다. 나머지 절반은 쓰기 경로가 아예 없는 수집기 인터페이스다.
#
# 검사 방식은 금지 목록이 아니라 ALLOW-LIST다. "Delete|Modify|Put|..."처럼 금지 동사를
# 나열하면, 아무도 생각하지 못한 동사로 된 쓰기 API가 AWS에 추가되는 순간 뚫린다. 대신
# 이름이 알려진 조회 접두사로 시작하는 AWS 오퍼레이션만 허용한다.
#
# AWS SDK를 import하는 파일에서 검사하는 대상:
#   1. 오퍼레이션 호출        client.DescribeInstances(ctx, in)
#   2. 페이지네이터 생성자    ec2.NewDescribeInstancesPaginator(client, in)
#   3. 줄 끝에서 열린 호출    인자가 길어 gofmt가 다음 줄로 넘긴 경우
#   4. 인터페이스에 선언된 메서드 이름 (수집기별 좁은 API 타입)
#
# 사용법:
#   scripts/verify-readonly.sh [경로]      트리 검사 (기본값: 저장소 루트)
#   scripts/verify-readonly.sh --self-test 가드가 위반을 아직 잡아내는지 확인

set -euo pipefail

# AWS 오퍼레이션에 허용되는 조회 접두사.
#
# "Lookup"이 있는 이유는 cloudtrail:LookupEvents다. Describe/List/Get으로 시작하지 않지만
# 조회다. 추가할 것이 있으면 여기에 명시적으로, 이유를 주석으로 적어서 넣는다. 절대
# 포괄적인 예외로 처리하지 않는다.
ALLOWED_PREFIXES='Describe|List|Get|Lookup|Search|BatchGet'

# ctx를 첫 인자로 받지만 AWS 오퍼레이션이 아닌 것들. 모양만으로는 SDK 조회 호출과
# 구별할 수 없다. 이 목록은 짧게 유지한다. 항목 하나하나가 검사의 구멍이므로, 내부
# 메서드 이름을 AWS 오퍼레이션 작명과 겹치지 않게 짓는 편이 낫다.
#
# 항목별 근거:
#   Collect/NextPage/Run   - 우리 수집기·러너·페이지네이터 메서드
#   Explain                - 에러를 사람이 읽는 문장으로 바꾸는 우리 함수
#   Config/ConfigWithLocations/WhoAmI
#                          - 우리 awsclient 함수. Config 계열은 로컬 설정 로드(파일 읽기),
#                            WhoAmI는 내부에서 GetCallerIdentity(조회)를 부른다
#   LoadDefaultConfig      - AWS SDK 설정 로더. 자격증명 체인을 구성할 뿐 리소스를
#                            조회하지 않는다
#   CommandContext         - os/exec 표준 라이브러리. aws CLI 대조에 쓴다
#   Value/Retrieve/Do      - SDK 자격증명 provider와 HTTP client의 표준 메서드
#   recordSets             - route53 수집기의 내부 메서드. 내부에서 ListResourceRecordSets
#                            (조회)를 부른다. ctx를 첫 인자로 받아 모양이 SDK 호출과 같다
#   targetHealth           - elbv2 타깃그룹 수집기의 내부 메서드. DescribeTargetHealth
#                            (조회)를 감싼다
#   webACLToResource       - wafv2 수집기의 내부 메서드. 요약을 리소스로 바꾸며 내부에서
#                            GetWebACL(조회)을 부른다
#   FanOut                 - collect의 팬아웃 헬퍼. 호출자가 넘긴 함수를 상한 있는 동시성으로
#                            실행할 뿐 AWS를 직접 부르지 않는다. 실제 호출은 호출자 쪽에서
#                            이 검사를 받는다
#   keyEntries             - kms 수집기의 내부 메서드. ListKeys(조회)를 페이지째 감싼다
#   aliasesByKeyID         - kms 수집기의 내부 메서드. ListAliases(조회)를 감싸 키 ID로
#                            색인한다
#   clusterARNs            - ecs 수집기의 내부 메서드. ListClusters(조회)를 페이지째 감싸
#                            클러스터 ARN 목록을 만든다
#   servicesForCluster     - ecs 서비스 수집기의 내부 메서드. ListServices와
#                            DescribeServices(둘 다 조회)를 클러스터 단위로 감싼다
#   taskDefinitionARNs     - ecs 태스크 정의 수집기의 내부 메서드. ListTaskDefinitions
#                            (조회)를 페이지째 감싼다
#   clusterNames           - eks 수집기들의 내부 메서드. ListClusters(조회)를 페이지째 감싸
#                            클러스터 이름 목록을 만든다
#   nodegroupsForCluster   - eks 노드그룹 수집기의 내부 메서드. ListNodegroups와
#                            DescribeNodegroup(둘 다 조회)을 클러스터 단위로 감싼다
#   profilesForCluster     - eks 파게이트 프로파일 수집기의 내부 메서드. ListFargateProfiles와
#                            DescribeFargateProfile(둘 다 조회)을 클러스터 단위로 감싼다
#   tableNames             - dynamodb 수집기의 내부 메서드. ListTables(조회)를 페이지째 감싼다
#   topicARNs              - sns 수집기의 내부 메서드. ListTopics(조회)를 페이지째 감싼다
#   queueURLs              - sqs 수집기의 내부 메서드. ListQueues(조회)를 페이지째 감싼다
INTERNAL_ALLOW='Collect|NextPage|Run|Explain|Config|ConfigWithLocations|WhoAmI|LoadDefaultConfig|CommandContext|Value|Retrieve|Do|recordSets|targetHealth|webACLToResource|FanOut|keyEntries|aliasesByKeyID|clusterARNs|servicesForCluster|taskDefinitionARNs|clusterNames|nodegroupsForCluster|profilesForCluster|tableNames|topicARNs|queueURLs'

usage() {
  sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
}

# is_allowed <오퍼레이션 이름>
is_allowed() {
  local name="$1"

  if printf '%s' "$name" | grep -qE "^(${ALLOWED_PREFIXES})[A-Z0-9]"; then
    return 0
  fi

  # 일부 서비스에는 접두사 자체가 온전한 오퍼레이션인 경우가 있다(예: Get, List).
  if printf '%s' "$name" | grep -qE "^(${ALLOWED_PREFIXES})$"; then
    return 0
  fi

  if printf '%s' "$name" | grep -qE "^(${INTERNAL_ALLOW})$"; then
    return 0
  fi

  return 1
}

# aws_files <루트> — AWS SDK를 참조하는 Go 파일 목록.
aws_files() {
  local root="$1"

  find "$root" -type f -name '*.go' \
    -not -path '*/vendor/*' \
    -not -path '*/.git/*' \
    -print0 |
    xargs -0 grep -lE '"github\.com/aws/aws-sdk-go-v2' 2>/dev/null || true
}

# report <파일> <줄> <이름> <사유>
report() {
  printf '%s:%s: 금지된 AWS 오퍼레이션 %s (%s)\n' "$1" "$2" "$3" "$4" >&2
}

# scan <루트> — 위반이 있으면 0이 아닌 값을 반환한다.
scan() {
  local root="$1"
  local violations=0
  local files
  files="$(aws_files "$root")"

  if [ -z "$files" ]; then
    printf 'verify-readonly: 아직 AWS SDK를 import하는 파일이 없어 검사할 대상이 없습니다\n'
    return 0
  fi

  local file line name hit
  while IFS= read -r file; do
    [ -n "$file" ] || continue

    # 1. ctx를 첫 인자로 받는 오퍼레이션 호출.
    while IFS= read -r hit; do
      [ -n "$hit" ] || continue
      line="${hit%%:*}"
      name="$(printf '%s' "${hit#*:}" | sed -E 's/^\.([A-Za-z_][A-Za-z0-9_]*).*/\1/')"

      if ! is_allowed "$name"; then
        report "$file" "$line" "$name" "ctx 호출"
        violations=$((violations + 1))
      fi
    done < <(grep -noE '\.[A-Za-z_][A-Za-z0-9_]*\([[:space:]]*ctx[,)]' "$file" || true)

    # 2. 페이지네이터 생성자. 오퍼레이션 이름이 New와 Paginator 사이에 있다.
    while IFS= read -r hit; do
      [ -n "$hit" ] || continue
      line="${hit%%:*}"
      name="$(printf '%s' "${hit#*:}" | sed -E 's/^New([A-Za-z0-9_]*)Paginator.*/\1/')"

      if ! is_allowed "$name"; then
        report "$file" "$line" "$name" "페이지네이터"
        violations=$((violations + 1))
      fi
    done < <(grep -noE 'New[A-Za-z0-9_]+Paginator\(' "$file" || true)

    # 3. gofmt가 인자를 다음 줄로 넘긴 호출.
    while IFS= read -r hit; do
      [ -n "$hit" ] || continue
      line="${hit%%:*}"
      name="$(printf '%s' "${hit#*:}" | sed -E 's/^\.([A-Za-z_][A-Za-z0-9_]*).*/\1/')"

      if ! is_allowed "$name"; then
        report "$file" "$line" "$name" "줄바꿈된 호출"
        violations=$((violations + 1))
      fi
    done < <(grep -noE '\.[A-Z][A-Za-z0-9_]*\([[:space:]]*$' "$file" || true)

    # 4. 인터페이스에 선언된 메서드 이름. 모든 AWS 접근은 수집기별 좁은 인터페이스를
    #    거치므로, 그 메서드 집합이 두 번째 검문소가 된다.
    while IFS= read -r hit; do
      [ -n "$hit" ] || continue
      line="${hit%%:*}"
      name="${hit#*:}"

      if ! is_allowed "$name"; then
        report "$file" "$line" "$name" "인터페이스 메서드"
        violations=$((violations + 1))
      fi
    done < <(awk '
      /interface[[:space:]]*\{/ { depth = 1; next }
      depth > 0 {
        if ($0 ~ /\}/) { depth = 0; next }
        if (match($0, /^[[:space:]]*[A-Z][A-Za-z0-9_]*\(/)) {
          name = substr($0, RSTART, RLENGTH - 1)
          gsub(/[[:space:]]/, "", name)
          print NR ":" name
        }
      }
    ' "$file" || true)
  done <<EOF
$files
EOF

  if [ "$violations" -gt 0 ]; then
    cat >&2 <<'MSG'

verify-readonly: 실패

cloudloupe는 설계상 조회 전용입니다. Describe, List, Get, Lookup, Search, BatchGet으로
시작하는 AWS 오퍼레이션만 허용됩니다.

실제로 조회인 API가 이름 때문에 거부되었다면, 같은 커밋에서 scripts/verify-readonly.sh의
ALLOWED_PREFIXES에 접두사를 추가하고 왜 그것이 조회인지 주석으로 남기세요.
MSG
    return 1
  fi

  printf 'verify-readonly: 통과 (파일 %s개 검사)\n' "$(printf '%s\n' "$files" | grep -c .)"
  return 0
}

# self_test는 스캐너가 여전히 실패할 수 있음을 증명한다. 절대 실패하지 않는 가드는 없는
# 가드보다 위험하다. 신뢰받기 때문이다.
self_test() {
  local tmp status=0
  tmp="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT

  mkdir -p "$tmp/bad" "$tmp/good"

  cat >"$tmp/bad/writer.go" <<'GO'
package bad

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func doIt(ctx context.Context, client *ec2.Client) {
	_, _ = client.TerminateInstances(ctx, nil)
}
GO

  cat >"$tmp/bad/paginated.go" <<'GO'
package bad

import "github.com/aws/aws-sdk-go-v2/service/ec2"

func paginate(client *ec2.Client) {
	_ = ec2.NewDeleteSnapshotsPaginator(client, nil)
}
GO

  cat >"$tmp/bad/iface.go" <<'GO'
package bad

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type writerAPI interface {
	ModifyInstanceAttribute(context.Context, *ec2.ModifyInstanceAttributeInput) error
}
GO

  cat >"$tmp/good/reader.go" <<'GO'
package good

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type instancesAPI interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
}

func read(ctx context.Context, client *ec2.Client) {
	_, _ = client.DescribeInstances(ctx, nil)
	_ = ec2.NewDescribeVolumesPaginator(client, nil)
	_, _ = client.DescribeNetworkInterfaces(
		ctx,
		nil,
	)
}
GO

  printf '자체 검사: 실패해야 하는 트리를 검사합니다\n'
  if scan "$tmp/bad" >/dev/null 2>&1; then
    printf '자체 검사: 실패 — 쓰기 호출이 있는 코드를 스캐너가 통과시켰습니다\n' >&2
    status=1
  else
    printf '자체 검사: 통과, 위반을 검출했습니다\n'
  fi

  printf '자체 검사: 통과해야 하는 트리를 검사합니다\n'
  if scan "$tmp/good" >/dev/null 2>&1; then
    printf '자체 검사: 통과, 조회 전용 코드를 정상 수용했습니다\n'
  else
    printf '자체 검사: 실패 — 조회 전용 코드를 스캐너가 거부했습니다\n' >&2
    scan "$tmp/good" || true
    status=1
  fi

  return "$status"
}

main() {
  case "${1:-}" in
    --self-test)
      self_test
      ;;
    -h | --help)
      usage
      ;;
    *)
      local root="${1:-}"
      if [ -z "$root" ]; then
        root="$(git rev-parse --show-toplevel 2>/dev/null || printf '.')"
      fi
      scan "$root"
      ;;
  esac
}

main "$@"
