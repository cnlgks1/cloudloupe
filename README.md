<h1 align="center">cloudloupe</h1>

<p align="center">
  <em>A read-only AWS infrastructure investigation TUI — inspect resources, relationships, and evidence across profiles.</em>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go version" src="https://img.shields.io/badge/go-1.25%2B-00ADD8.svg">
  <img alt="상태" src="https://img.shields.io/badge/status-개발%20중-orange.svg">
</p>

AWS 인프라를 조회하는 터미널 UI입니다. `~/.aws/config`의 프로필을 읽어 여러 프로필·리전의
리소스를 수집하고, 리소스 필드 상세와 수집기가 기록한 관계 메타데이터를 보여줍니다.

> **개발 중.** 6개 그룹 16개 타입 수집과 TUI 조회는 동작합니다.
> 그래프 UI, 미사용 탐지, 리포트는 [로드맵](#로드맵) 3~5단계입니다.

## 빠른 시작

```sh
git clone https://github.com/cnlgks1/cloudloupe.git
cd cloudloupe
make build
./cloudloupe
```

프로필 선택 → 계정 확인(STS) → 리전 선택 → 리소스 선택 → 조회 순으로 진행합니다.

## 조회 전용

AWS 리소스를 생성·수정·삭제하지 않습니다.

- SDK 호출은 allow-list로 제한합니다. `Describe`, `List`, `Get`, `Lookup`, `Search`,
  `BatchGet` 접두사만 허용하고, 그 외 호출이 있으면 `make verify-readonly`가 실패합니다.
- 수집기 인터페이스에는 타입 식별용 `Type`과 조회 동작 `Collect`만 있습니다.
- 설정 파일은 파싱만 하고 수정하거나 전송하지 않습니다.
- 현재 실행 경로는 로컬 파일을 쓰지 않습니다.

## 지원 리소스

TUI에서는 그룹을 고른 뒤 세부 항목 화면에서 조회 대상을 정합니다. 그룹 전체가 필요하면
그룹 화면에서 `space`로 고릅니다. 모든 조회는 아래 AWS SDK for Go v2 API만 사용합니다.

| 그룹 | 타입 ID | SDK API |
| --- | --- | --- |
| EC2 | `ec2:instance` | `ec2.DescribeInstances` |
| EC2 | `ec2:volume` | `ec2.DescribeVolumes` |
| EC2 | `ec2:networkInterface` | `ec2.DescribeNetworkInterfaces` |
| EC2 | `ec2:address` | `ec2.DescribeAddresses` |
| VPC | `ec2:vpc` | `ec2.DescribeVpcs` |
| VPC | `ec2:subnet` | `ec2.DescribeSubnets` |
| VPC | `ec2:securityGroup` | `ec2.DescribeSecurityGroups` |
| 네트워크 | `ec2:routeTable` | `ec2.DescribeRouteTables` |
| 네트워크 | `ec2:internetGateway` | `ec2.DescribeInternetGateways` |
| 네트워크 | `ec2:natGateway` | `ec2.DescribeNatGateways` |
| 네트워크 | `ec2:vpcEndpoint` | `ec2.DescribeVpcEndpoints` |
| ELB | `elbv2:loadBalancer` | `elbv2.DescribeLoadBalancers` |
| ELB | `elbv2:listener` | `elbv2.DescribeLoadBalancers`, `DescribeListeners`, `DescribeRules` |
| ELB | `elbv2:targetGroup` | `elbv2.DescribeTargetGroups`, `DescribeTargetHealth` |
| Route 53 | `route53:recordSet` | `route53.ListHostedZones`, `ListResourceRecordSets` |
| WAF | `wafv2:webAcl` | `wafv2.ListWebACLs`, `GetWebACL` (REGIONAL 스코프) |

`elbv2`는 SDK 패키지 `elasticloadbalancingv2`입니다. Route 53은 글로벌 서비스라 리전 선택과
무관하게 한 번만 조회합니다.

타입 ID는 관계 식별과 수집기 선택에 쓰이는 내부 계약입니다.

## 사용법

```sh
cloudloupe                        # 대화형 TUI
cloudloupe --ascii                # 유니코드 미지원 터미널용 ASCII 테마
cloudloupe --list-profiles        # TUI 없이 프로필 목록
cloudloupe --output json          # 같은 목록을 JSON으로
cloudloupe --check                # 설정 위치·권한 진단 (문제 시 exit != 0)
cloudloupe --config PATH --credentials PATH
```

터미널이 아니거나 파이프로 넘기면 목록 출력으로 자동 폴백합니다.

주요 키: `↑/↓` 또는 `j/k` 이동, `space` 다중 선택, `enter/→` 다음, `esc/←` 뒤로(수집 중에는
취소), `q` 종료. 리소스 선택은 그룹 → 세부 항목 두 단계입니다. 어느 화면에서든 `enter`는
커서가 가리키는 것만 조회하고, `space`로 체크하면 여러 개를 함께 조회합니다. 그룹 화면에서
`space`로 그룹을 체크하면 세부 항목을 건너뛰고 그룹 전체를 조회합니다. 리소스 목록에서는
`t` 종류 필터, `/` 텍스트 검색, `e` 부분 오류 보기, `p` 프로필 전환, `r` 리전 전환.

> **경로 플래그의 적용 범위.** `--config`, `--credentials`와 TUI 경로 입력은 **프로필 탐색에만**
> 적용됩니다. STS 확인과 리소스 조회에도 같은 파일을 쓰려면 `AWS_CONFIG_FILE`,
> `AWS_SHARED_CREDENTIALS_FILE`을 설정하세요. `--check`, `--list-profiles`, `--output`도
> 경로 플래그를 적용하지 않습니다.

## 설정 해석

경로는 빌드 시점에 굽지 않고 실행할 때마다 해석합니다. 설치 방식과 무관하게 실행한 사용자의
환경과 홈 디렉터리를 씁니다. 우선순위는 AWS CLI와 동일합니다.

| 대상 | 우선순위 |
| --- | --- |
| config | `AWS_CONFIG_FILE` → `~/.aws/config` |
| credentials | `AWS_SHARED_CREDENTIALS_FILE` → `~/.aws/credentials` |
| 기본 프로필 | `AWS_PROFILE` → `AWS_DEFAULT_PROFILE` |
| 기본 리전 | `AWS_REGION` → `AWS_DEFAULT_REGION` |

Windows에서는 `%USERPROFILE%`, 비어 있으면 `%HOMEDRIVE%%HOMEPATH%`를 씁니다.

`--check`는 해석된 경로와 그 근거, 읽기 가능 여부, 끊어진 심볼릭 링크, 존재하지 않는
`AWS_PROFILE`, 열려 있는 자격증명 파일 권한, 다른 위치의 설정(snap AWS CLI, sudo 실행 등)을
보고합니다. `aws configure list-profiles`가 있으면 참고로 목록을 대조하지만, 목록이 일치해도
두 프로그램이 같은 파일을 읽었다는 증명은 아닙니다.

## IAM 권한

읽기 권한만 필요합니다. 관리형 `ReadOnlyAccess`면 충분합니다. 더 좁게 가려면
[`examples/iam/cloudloupe-readonly-policy.json`](examples/iam/cloudloupe-readonly-policy.json)을
출발점으로 줄이세요. 이 예제는 로드맵의 CloudWatch·CloudTrail·태그 증거 API까지 포함하므로
현재 호출 기준의 엄격한 최소 권한은 아닙니다.

권한이 부족해도 전체가 죽지 않습니다. 읽을 수 없는 리전이나 타입은 화면에 오류로 보고되고
나머지는 정상 수집됩니다.

## 설치

지금 재현 가능한 방법은 위의 소스 빌드입니다. `v0.1.0` 같은 태그를 push하면 CI 통과 후
GitHub Release와 `checksums.txt`를 게시하도록 구성되어 있고, 아래 설치 방법은 대응하는
Release가 실제로 있어야 동작합니다.

```sh
# macOS, Linux, WSL: OS·CPU 자동 감지, SHA-256 검증, ~/.local/bin에 설치
curl -fsSL https://raw.githubusercontent.com/cnlgks1/cloudloupe/main/install.sh | sh

# 버전·경로 지정
CLOUDLOUPE_VERSION=v0.1.0 CLOUDLOUPE_INSTALL_DIR="$HOME/bin" sh install.sh
```

파이프로 실행하기 전에 스크립트를 읽고 싶다면 `curl -fsSLO .../install.sh && less install.sh`
후 `sh install.sh`를 실행하세요.

공개 태그 이후에는 Release 자산과 무관하게 Go 도구 체인으로도 설치할 수 있습니다.

```sh
go install github.com/cnlgks1/cloudloupe/cmd/cloudloupe@latest
```

Windows는 [Releases](https://github.com/cnlgks1/cloudloupe/releases)에서
`cloudloupe_windows_{amd64,arm64}.zip`과 `checksums.txt`를 받아
`Get-FileHash -Algorithm SHA256`으로 검증한 뒤 `cloudloupe.exe`를 PATH에 둡니다.

Homebrew는 첫 Release 후 공개 태그의 소스를 로컬에서 빌드하는 Formula로 제공할 예정입니다.
`brew install cnlgks1/tap/cloudloupe`를 검증한 뒤 설치 방법을 공개하며, 관리 절차는
[RELEASING.md](RELEASING.md#homebrew-formula-게시)에 있습니다.

### 지원 플랫폼

macOS, Linux, Windows × amd64, arm64 6종을 `CGO_ENABLED=0` 정적 바이너리로 빌드합니다.
유니코드 박스 문자를 못 그리는 터미널은 자동 감지해 ASCII로 폴백하며, `--ascii` 또는
`CLOUDLOUPE_ASCII=1`로 강제할 수 있습니다.

## 로드맵

| 단계 | 범위 | 상태 |
| --- | --- | --- |
| 1 | 프로필·신원·리전 선택과 EC2 인스턴스 조회 TUI | 완료 |
| 2 | EBS, ENI, EIP, VPC, 서브넷, 보안 그룹, 라우팅 테이블, 인터넷·NAT 게이트웨이, VPC 엔드포인트, ALB/NLB, 타깃 그룹, Route 53, WAF | 완료 |
| 3 | 관계 그래프 코어와 TUI 그래프 탐색 연결 | 진행 중 |
| 4 | 근거·신뢰도를 갖춘 미사용 후보 탐지 (CloudWatch, CloudTrail) | 예정 |
| 5 | JSON / CSV / Markdown 리포트 | 예정 |
| 6 | GoReleaser Release·체크섬, 설치 스크립트, Homebrew Formula | 진행 중 |

4단계 판정 결과에는 **확정**, **추정**, **확인 필요** 신뢰도와 판단에 사용한 API 응답·지표를
함께 붙입니다.

## 소스 구조

```text
cmd/cloudloupe            플래그, 입출력, TUI 실행
internal/app              프로필·리전별 AWS 설정과 수집 실행 조립
internal/catalog          타입 ID·표시명·범위·테이블 열·수집기 생성의 단일 출처
internal/collect          AWS SDK를 모르는 Collector/Registry/Runner 코어
internal/collector/<svc>  서비스별 조회와 SDK → model 변환
internal/graph            ID·ARN·DNS 관계 해석과 정방향·역방향 인덱스
internal/model            외부 의존성이 없는 도메인 모델
internal/tui              Bubble Tea 상태 전이와 렌더링
```

새 리소스를 추가하는 절차는 좁은 조회 인터페이스와 자격증명 없는 테스트 대역을 만들고,
`internal/catalog`에 정의를 등록한 뒤 README 표와 IAM 예제를 갱신하는 것입니다.
`internal/collect`와 TUI 화면 전이는 손대지 않습니다. `internal/graph` 코어는 사용할 수
있지만 TUI 연결은 로드맵 3단계입니다.

## 개발

```sh
make test       # 단위 테스트
make ci         # gofmt 검사, vet, 조회 전용 가드, 테스트, 현재 플랫폼 빌드
make lint       # golangci-lint
make test-race  # 데이터 레이스 검사
make cross      # 6종 OS/아키텍처 빌드
make snapshot   # 태그 없이 GoReleaser 산출물 검사
make help       # 전체 타깃
```

`make ci`는 로컬 묶음 검사이며 GitHub Actions 행렬과 동일하지 않습니다.

기여 절차는 [CONTRIBUTING.md](CONTRIBUTING.md), 릴리스 운영은 [RELEASING.md](RELEASING.md),
설계 규칙은 [docs/go-conventions.md](docs/go-conventions.md)에 있습니다.

## 라이선스

[MIT](LICENSE)
