<h1 align="center">cloudloupe</h1>

<p align="center">
  <em>A read-only AWS infrastructure investigation TUI — inspect resources, relationships, and evidence across profiles.</em>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go version" src="https://img.shields.io/badge/go-1.25%2B-00ADD8.svg">
  <img alt="상태" src="https://img.shields.io/badge/status-개발%20중-orange.svg">
</p>

`~/.aws/config`의 프로필을 읽어 여러 프로필·리전의 AWS 리소스를 조회하는 터미널 UI입니다.
리소스를 만들거나 바꾸지 않습니다.

> **개발 중.** 16개 그룹 31개 타입 조회와 상세 화면의 관계 표시가 동작합니다. 미사용 탐지와
> 리포트는 아직 없습니다.

## 빠른 시작

```sh
git clone https://github.com/cnlgks1/cloudloupe.git
cd cloudloupe
make build
./cloudloupe
```

프로필 → 계정 확인(STS) → 리전 → 리소스 → 조회 순으로 진행합니다.

## 조회 전용

- SDK 호출은 allow-list로 제한합니다. `Describe`, `List`, `Get`, `Lookup`, `Search`,
  `BatchGet` 접두사만 허용하고, 그 외 호출이 있으면 `make verify-readonly`가 실패합니다.
- 수집기 인터페이스에는 `Type`과 `Collect`만 있습니다. 쓰기 경로가 없습니다.
- 설정 파일은 파싱만 합니다. 수정하거나 전송하지 않습니다.
- 현재 실행 경로는 로컬 파일을 쓰지 않습니다.

권한이 부족해도 전체가 죽지 않습니다. 읽을 수 없는 리전이나 타입은 오류로 보고되고 나머지는
정상 수집됩니다.

## 지원 리소스

| 서비스 | 리소스 타입 | SDK API |
| --- | --- | --- |
| EC2 | `ec2:instance` | `ec2.DescribeInstances` |
| EC2 | `ec2:volume` | `ec2.DescribeVolumes` |
| EC2 | `ec2:networkInterface` | `ec2.DescribeNetworkInterfaces` |
| EC2 | `ec2:address` | `ec2.DescribeAddresses` |
| VPC | `ec2:vpc` | `ec2.DescribeVpcs` |
| VPC | `ec2:subnet` | `ec2.DescribeSubnets` |
| VPC | `ec2:securityGroup` | `ec2.DescribeSecurityGroups` |
| Network | `ec2:routeTable` | `ec2.DescribeRouteTables` |
| Network | `ec2:internetGateway` | `ec2.DescribeInternetGateways` |
| Network | `ec2:natGateway` | `ec2.DescribeNatGateways` |
| Network | `ec2:vpcEndpoint` | `ec2.DescribeVpcEndpoints` |
| ELB | `elbv2:loadBalancer` | `elbv2.DescribeLoadBalancers` |
| ELB | `elbv2:listener` | `elbv2.DescribeLoadBalancers`, `DescribeListeners`, `DescribeRules` |
| ELB | `elbv2:targetGroup` | `elbv2.DescribeTargetGroups`, `DescribeTargetHealth` |
| Auto Scaling | `autoscaling:autoScalingGroup` | `autoscaling.DescribeAutoScalingGroups` |
| Lambda | `lambda:function` | `lambda.ListFunctions` |
| ECS | `ecs:cluster` | `ecs.ListClusters`, `DescribeClusters` |
| ECS | `ecs:service` | `ecs.ListClusters`, `ListServices`, `DescribeServices` |
| ECS | `ecs:taskDefinition` | `ecs.ListTaskDefinitions`, `DescribeTaskDefinition` |
| ECR | `ecr:repository` | `ecr.DescribeRepositories` |
| EKS | `eks:cluster` | `eks.ListClusters`, `DescribeCluster` |
| EKS | `eks:nodegroup` | `eks.ListClusters`, `ListNodegroups`, `DescribeNodegroup` |
| EKS | `eks:fargateProfile` | `eks.ListClusters`, `ListFargateProfiles`, `DescribeFargateProfile` |
| RDS | `rds:dbCluster` | `rds.DescribeDBClusters` |
| RDS | `rds:dbInstance` | `rds.DescribeDBInstances` |
| DynamoDB | `dynamodb:table` | `dynamodb.ListTables`, `DescribeTable` |
| Route 53 | `route53:recordSet` | `route53.ListHostedZones`, `ListResourceRecordSets` |
| WAF | `wafv2:webAcl` | `wafv2.ListWebACLs`, `GetWebACL` (REGIONAL 스코프) |
| IAM | `iam:role` | `iam.ListRoles` |
| KMS | `kms:key` | `kms.ListKeys`, `DescribeKey`, `ListAliases` |
| S3 | `s3:bucket` | `s3.ListBuckets` |

Route 53과 IAM은 글로벌 서비스라 리전 선택과 무관하게 한 번만 조회하고 리전이 `global`로
표시됩니다. 나머지는 선택한 리전마다 조회합니다.

화면의 필드 이름과 값은 SDK 응답을 그대로 씁니다. 이름은 구조체 필드 이름
(`InstanceType`, `PrivateIpAddress`), 값은 API 값(`available`, `gp3`, `true`), 시각은
RFC 3339입니다. `aws` CLI 출력과 그대로 대조할 수 있게 하려는 것입니다.

시각 표시는 두 갈래입니다. 목록에는 `kubectl`처럼 경과 시간을 `Age` 열로 보여주고
(`5h`, `30d`, `1y35d`), 상세에는 실행한 사람의 지역 시간을 오프셋과 함께 보여줍니다
(`2025-11-14T12:22:05+09:00`). UTC로 보려면 `TZ=UTC cloudloupe`로 실행하세요.

항목마다 추가 호출이 필요한 값은 아직 가져오지 않습니다. 그래서 열은 있어도 값이 `-`로 비는
것이 있습니다. IAM 역할의 `RoleLastUsed`·`PermissionsBoundary`·태그, KMS 키 태그, S3 버킷의
암호화·퍼블릭 액세스 차단·버전 관리·태그, Lambda 함수 태그입니다. API 스로틀링을 피하려는
것입니다.

상세 화면(목록에서 `enter`)은 관계를 함께 보여줍니다. 관계 이름은 그 연결을 만든 SDK 응답
필드 경로(`DBClusterIdentifier`, `VpcConfig.SubnetIds`, `Routes.NatGatewayId`)라 `aws` CLI
출력과 대조할 수 있습니다. 대상은 타입과 이름으로 표시합니다. 대상 타입을 같이 조회하지
않았으면 이름을 알 수 없으므로 ID만 보여주며, 그 타입도 함께 조회하면 이름이 채워집니다.
`Referenced by`는 그 리소스를 가리키는 다른 리소스입니다. AWS에는 역방향을 알려주는 API가
없어 추가 호출 없이 조회 결과에서 계산합니다.

## 사용법

```sh
cloudloupe                                    # 대화형 TUI
cloudloupe --version                          # 버전·커밋 출력
cloudloupe --ascii                            # 유니코드 미지원 터미널용 ASCII 테마
cloudloupe --check                            # 설정 위치·권한 진단 (문제 시 exit != 0)
cloudloupe --list-profiles [--output json]    # TUI 없이 프로필 목록
cloudloupe --config PATH --credentials PATH
```

터미널이 아니거나 파이프로 넘기면 목록 출력으로 자동 폴백합니다.

| 키 | 동작 |
| --- | --- |
| `↑↓` `j` `k` | 이동 |
| `enter` `→` | 다음 단계 · 커서 항목 조회 · 상세 열기 |
| `space` | 여러 개 선택 |
| `esc` `←` | 뒤로 (수집 중에는 취소) |
| `t` `/` `e` | 종류 필터 · 텍스트 검색 · 부분 오류 보기 |
| `p` `r` | 프로필 전환 · 리전 전환 |
| `q` | 종료 |
| `ctrl+c` | 어디서든 즉시 종료 |

리소스 선택은 한 화면에서 끝납니다. 서비스가 접힌 목록으로 나오고 `→`로 그 자리에서 펼칩니다.
`enter`는 커서가 서비스 줄이면 그 서비스 전체를, 리소스 타입 줄이면 그 타입만 조회합니다.
`space`로 여러 개를 체크하면 서비스를 넘어 함께 조회하며, 접어도 선택은 유지됩니다.

리소스 타입이 하나뿐인 서비스는 펼칠 것이 없으므로 펼침 표시가 없고, 그 타입 ID를 서비스
줄에 바로 보여줍니다. 그 줄에서 `→`는 펼치는 대신 조회로 넘어갑니다.

| 선택 화면 키 | 동작 |
| --- | --- |
| `→` `←` | 서비스 펼치기 · 접기 (펼칠 것이 없으면 `→`는 조회) |
| `z` | 전부 접기 |
| `/` | 검색 (서비스·타입 이름·타입 ID) |
| `a` | 검색으로 걸러진 것 전체 선택 |
| `x` | 선택 비우기 |

검색 중에는 트리 대신 `Service` 열이 붙은 평면 목록이 됩니다. 서비스가 늘어나도 펼치지 않고
바로 좁힐 수 있습니다. `a`는 검색 중에만 동작합니다. 필터 없이 전부 선택하면 리소스 타입 수 ×
리전 수만큼 요청이 생기기 때문입니다.

`Resource types` 열은 그 서비스에서 cloudloupe가 조회할 수 있는 리소스 타입 수입니다. 리전에
있는 리소스 개수가 아닙니다. 실제 개수는 조회 결과의 `N resources`입니다.

`t`는 결과에 종류가 둘 이상일 때, `e`는 부분 오류가 있을 때만 동작합니다. `/` 검색 입력 중에는
`q`, `p`, `r`도 검색어로 들어갑니다.

`--config`, `--credentials`와 TUI에서 입력한 경로는 프로필 탐색, STS 신원 확인, 실제
리소스 조회에 동일하게 적용됩니다.

## 설정 해석

경로는 실행할 때마다 해석합니다. 우선순위는 AWS CLI와 같습니다.

| 대상 | 우선순위 |
| --- | --- |
| config | `AWS_CONFIG_FILE` → `~/.aws/config` |
| credentials | `AWS_SHARED_CREDENTIALS_FILE` → `~/.aws/credentials` |
| 기본 프로필 | `AWS_PROFILE` → `AWS_DEFAULT_PROFILE` |
| 기본 리전 | `AWS_REGION` → `AWS_DEFAULT_REGION` |

Windows에서는 `%USERPROFILE%`, 비어 있으면 `%HOMEDRIVE%%HOMEPATH%`를 씁니다.

`--check`는 해석된 경로와 근거, 읽기 가능 여부, 끊어진 심볼릭 링크, 존재하지 않는
`AWS_PROFILE`, 열려 있는 자격증명 파일 권한을 보고합니다.

## IAM 권한

읽기 권한만 필요합니다. 관리형 `ReadOnlyAccess`면 충분합니다. 더 좁게 가려면
[`examples/iam/cloudloupe-readonly-policy.json`](examples/iam/cloudloupe-readonly-policy.json)을
출발점으로 줄이세요. 이 예제는 아직 쓰지 않는 CloudWatch·CloudTrail·태그 API까지 포함하므로
현재 호출 기준의 최소 권한은 아닙니다.

## 설치

지금 재현 가능한 방법은 위의 소스 빌드입니다. `v*` 태그를 push하면 CI 통과 후 GitHub
Release와 `checksums.txt`를 게시하도록 구성되어 있고, 아래 방법은 대응하는 Release가 있어야
동작합니다.

```sh
# macOS, Linux, WSL: OS·CPU 자동 감지, SHA-256 검증, ~/.local/bin에 설치
curl -fsSL https://raw.githubusercontent.com/cnlgks1/cloudloupe/main/install.sh | sh

# Go 도구 체인
go install github.com/cnlgks1/cloudloupe/cmd/cloudloupe@latest
```

Windows는 [Releases](https://github.com/cnlgks1/cloudloupe/releases)에서 zip과
`checksums.txt`를 받아 `Get-FileHash -Algorithm SHA256`으로 검증한 뒤 PATH에 둡니다.

Homebrew Formula는 첫 Release 이후 제공할 예정입니다. 절차는
[RELEASING.md](RELEASING.md#homebrew-formula-게시)에 있습니다.

macOS, Linux, Windows × amd64, arm64 6종을 `CGO_ENABLED=0` 정적 바이너리로 빌드합니다.
유니코드 박스 문자를 못 그리는 터미널은 자동 감지해 ASCII로 폴백하며 `--ascii` 또는
`CLOUDLOUPE_ASCII=1`로 강제할 수 있습니다.

## 소스 구조

```text
cmd/cloudloupe            플래그, 입출력, TUI 실행
internal/app              프로필·리전별 AWS 설정과 수집 실행 조립
internal/catalog          타입 ID·표시명·범위·목록 열·수집기 생성의 단일 출처
internal/collect          AWS SDK를 모르는 Collector/Registry/Runner 코어
internal/collector/<svc>  서비스별 조회와 SDK → model 변환
internal/graph            ID·ARN·DNS 관계 해석과 정방향·역방향 인덱스
internal/model            외부 의존성이 없는 도메인 모델
internal/tui              Bubble Tea 상태 전이와 렌더링
```

## 개발

```sh
make ci         # gofmt 검사, vet, 조회 전용 가드, 테스트, 빌드, 6종 크로스 컴파일
make test       # 단위 테스트
make lint       # golangci-lint
make test-race  # 데이터 레이스 검사
make cross      # 6종 OS/아키텍처 빌드
make help       # 전체 타깃
```

`make ci`는 커밋 전 로컬 검사이며 GitHub Actions 행렬과 동일하지 않습니다. 크로스 컴파일은
로컬이 훨씬 싸므로(빌드 캐시가 있으면 몇 초) CI는 태그와 수동 실행에서만 돌립니다. 릴리스는
GoReleaser가 6종을 다시 빌드하고 하나라도 실패하면 게시하지 않습니다.

기여 절차는 [CONTRIBUTING.md](CONTRIBUTING.md), 릴리스 운영은 [RELEASING.md](RELEASING.md),
설계 규칙은 [docs/go-conventions.md](docs/go-conventions.md)에 있습니다.

## 라이선스

[MIT](LICENSE)
