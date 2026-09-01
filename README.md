<h1 align="center">cloudloupe</h1>

<p align="center">
  <em>A read-only AWS infrastructure investigation TUI — inspect resources, relationships, and evidence across profiles.</em>
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go version" src="https://img.shields.io/badge/go-1.25%2B-00ADD8.svg">
  <img alt="상태" src="https://img.shields.io/badge/status-개발%20중-orange.svg">
</p>

<!--
CI·Go Report Card 배지는 저장소를 공개로 전환한 뒤 추가한다. 비공개 상태에서는
badge.svg가 인증을 요구해 깨진 이미지로 보인다. 공개 전환 시 아래를 되살릴 것:
  [CI](actions/workflows/ci.yml/badge.svg)
  [Go Report Card](goreportcard.com/badge/github.com/cnlgks1/cloudloupe)
-->


---

> **개발 중입니다.** 대화형 TUI로 프로필을 고르고 EC2 인스턴스를 조회하는 흐름이
> 동작합니다. 나머지 리소스 타입과 관계 분석·미사용 탐지는 [로드맵](#로드맵)에 따라
> 추가되고 있습니다.

## 무엇인가

cloudloupe는 AWS 인프라를 **조사하기 위한** 터미널 UI입니다. `~/.aws/config`에 이미 있는
프로필을 그대로 읽어서 리전을 고르고, 실제로 배포된 것들을 훑어봅니다. 인스턴스, 볼륨,
네트워크 인터페이스, 로드밸런서, 타깃 그룹, DNS 레코드 같은 것들입니다. 그 리소스들이
서로 어떻게 연결되어 있는지 그려주고, 안 쓰는 것처럼 보이는 자원을 그 판단의 근거와 함께
표시합니다.

AWS 계정을 물려받아서 "이게 다 뭐고, 뭘 지워도 안전한가"를 답해야 하는 순간을 위한
도구입니다. 콘솔 탭을 스무 개 열지 않고요.

## 설계상 조회 전용

cloudloupe는 AWS 리소스를 생성하거나 수정하거나 삭제할 **수 없습니다**. 약속이 아니라
구조로 막아둔 것입니다.

- **금지 목록이 아니라 allow-list.** `Describe`, `List`, `Get`, `Lookup`, `Search`,
  `BatchGet`으로 시작하는 AWS SDK 호출만 허용하고, 나머지는 CI에서 빌드를 실패시킵니다
  (`make verify-readonly`). 금지 동사를 나열하는 방식은 AWS에 새 쓰기 API가 추가되는
  순간 뚫립니다.
- **타입 시스템에 쓰기 경로가 없습니다.** 수집기 인터페이스에는 `Collect` 메서드 하나뿐이라
  변경 동작이 들어앉을 자리가 없습니다.
- **자격증명은 읽기만 합니다.** `~/.aws/config`와 `~/.aws/credentials`를 파싱하되 수정하지
  않고, 어디로도 전송하지 않습니다.
- **현재 실행 경로는** 로컬 파일을 쓰지 않습니다.

필요한 IAM 권한은 읽기 권한뿐입니다. 최소 권한 정책은 [`examples/iam/`](examples/iam/)에
있습니다.

## 설치

아직 첫 공개 릴리스 태그는 없습니다. 지금 바로 동작하는 방법은 소스 빌드입니다.

```sh
git clone https://github.com/cnlgks1/cloudloupe.git
cd cloudloupe
make build
./cloudloupe
```

저장소를 공개하고 `v0.1.0` 같은 태그를 push하면 GitHub Actions가 전체 CI를 통과한 뒤 macOS,
Linux, Windows용 GitHub Release와 `checksums.txt`를 자동으로 게시합니다. 그때부터 아래 설치
방법이 동작합니다.

### macOS, Linux, Ubuntu, WSL

가장 간단한 설치 방법은 다음 한 줄입니다. 스크립트가 운영체제와 CPU를 자동으로 찾고,
릴리스의 SHA-256 체크섬을 검증한 뒤 기본적으로 `~/.local/bin/cloudloupe`에 설치합니다.

```sh
curl -fsSL https://raw.githubusercontent.com/cnlgks1/cloudloupe/main/install.sh | sh
```

실행하기 전에 설치 스크립트를 직접 확인하려면 내려받아 읽은 뒤 실행합니다.

```sh
curl -fsSLO https://raw.githubusercontent.com/cnlgks1/cloudloupe/main/install.sh
less install.sh
sh install.sh
```

특정 버전이나 다른 설치 경로도 지정할 수 있습니다.

```sh
CLOUDLOUPE_VERSION=v0.1.0 sh install.sh
CLOUDLOUPE_INSTALL_DIR="$HOME/bin" sh install.sh
```

### Go 도구 체인

```sh
go install github.com/cnlgks1/cloudloupe/cmd/cloudloupe@latest
```

저장소가 비공개인 동안에는 모듈 프록시를 우회하고 git 인증을 사용해야 합니다.

```sh
export GOPRIVATE=github.com/cnlgks1/*
go install github.com/cnlgks1/cloudloupe/cmd/cloudloupe@latest
```

### Windows

[GitHub Releases](https://github.com/cnlgks1/cloudloupe/releases)에서 CPU에 맞는
`cloudloupe_windows_amd64.zip` 또는 `cloudloupe_windows_arm64.zip`과 `checksums.txt`를
받아 SHA-256을 확인한 뒤 `cloudloupe.exe`를 PATH에 있는 디렉터리에 둡니다. PowerShell에서는
`Get-FileHash .\cloudloupe_windows_amd64.zip -Algorithm SHA256`으로 확인할 수 있습니다.

Homebrew는 별도 `homebrew-tap` 저장소와 그 저장소에 쓸 토큰이 필요합니다. GitHub Release가
실제로 게시된 뒤 tap을 연결하며, 그전에는 동작하지 않는 `brew install` 명령을 안내하지
않습니다. 저장소·토큰·서명 준비 순서는 [릴리스 운영 문서](RELEASING.md#homebrew-tap-연결)에
정리되어 있습니다.

## 지원 플랫폼

단일 정적 바이너리이며, CGO도 런타임 의존성도 없습니다.

| OS      | amd64 | arm64 |
| ------- | :---: | :---: |
| macOS   |   O   |   O   |
| Linux   |   O   |   O   |
| Windows |   O   |   O   |

유니코드 박스 문자를 렌더링하지 못하는 Windows 터미널을 위해 ASCII 폴백이 있습니다.
자동으로 감지하며, `--ascii`나 `CLOUDLOUPE_ASCII=1`로 강제할 수도 있습니다.

## 지원 리소스

TUI에서는 큰 AWS 리소스 단위로 선택하며, 내부에서는 아래 세부 타입을 함께 수집합니다.

| 선택 리소스 | 포함 세부 타입 | 상태 |
| --- | --- | :---: |
| EC2 | 인스턴스, EBS 볼륨, ENI, Elastic IP | O |
| VPC | VPC, 서브넷, 보안 그룹 | O |
| ELB | 로드 밸런서, 리스너·규칙 관계, 타깃 그룹과 타깃 상태 | O |
| Route 53 | 레코드 | O |
| WAF | Web ACL (REGIONAL) | O |

내부 타입 ID(`ec2:instance`, `ec2:volume`, `ec2:networkInterface`, `ec2:address`,
`ec2:vpc`, `ec2:subnet`, `ec2:securityGroup`, `elbv2:loadBalancer`, `elbv2:listener`,
`elbv2:targetGroup`, `route53:recordSet`, `wafv2:webAcl`)는 관계 식별과 수집기 선택에 그대로 사용합니다. 모든 조회는 `Describe`/`List`/`Get` 계열
API만 씁니다. 새 리소스는 서비스 그룹의 수집기와 카탈로그 정의를 추가하며, 조회 전용
가드가 쓰기 API를 자동으로 차단합니다.

관계 namespace와 ID·ARN·DNS 식별자 계약을 도입한 스냅샷은 `schemaVersion: 2`입니다.
v1 소비자는 키와 관계 ID 의미가 다른 v2 입력을 자동 변환하지 말고 버전을 확인해야 합니다.

## 사용법

```sh
# 대화형 TUI 시작: 프로필 선택 → 계정 확인 → 리전 선택 → 리소스 선택 → 조회
cloudloupe

# 설정이 기본 위치에 없으면 실행 중 경로를 입력받는다.
# 미리 지정할 수도 있다:
cloudloupe --config /path/to/config --credentials /path/to/credentials

# ASCII 테마 (구형 Windows 콘솔)
cloudloupe --ascii

# TUI 없이 프로필 목록만 (스크립트용)
cloudloupe --list-profiles
cloudloupe --output json

# 설정 위치 진단
cloudloupe --check
```

인자 없이 실행하면 TUI가 뜹니다. 파이프로 넘기거나 터미널이 아니면 자동으로 목록
출력으로 폴백합니다.

### TUI 흐름

```text
프로필 선택       ↑/↓ 또는 j/k 이동, enter/→ 선택, c 설정 경로 지정
계정 확인         선택한 프로필의 계정·사용자 확인 (STS)
리전 선택         ↑/↓ 또는 j/k 이동, space 다중 선택, enter/→ 다음
리소스 선택       ↑/↓ 또는 j/k 이동, space 다중 선택, enter/→ 조회
수집 중           esc로 취소
리소스 목록       t 종류 필터, / 텍스트 검색, e 부분 오류 보기, enter/→ 상세, p 프로필 전환, r 리전 전환, esc 뒤로, q 종료
종류 필터         실제 종류가 2개 이상일 때 전체/종류별 선택, enter/→ 적용, esc/← 취소
상세              ↑/↓ 또는 j/k 스크롤, esc/← 목록 복귀
수집 오류         리소스 종류·프로필·리전·AWS 오류 코드·설명 확인, enter/→ 원본 상세, esc/← 목록 복귀
오류 상세         사용자 설명과 원본 오류 확인, esc/←/q 오류 목록 복귀
```

프로필·리전·리소스 그룹·리소스 목록은 모두 컬럼 정렬 테이블로 표시됩니다. 상단 경로에는
현재 프로필, 리전과 선택한 리소스 그룹이 항상 표시됩니다.

`~/.aws/config`를 기본으로 보고, 없거나 다른 곳에 있으면 첫 화면에서 경로를 입력받습니다.

### 설정을 어디서 읽는가

경로는 빌드 시점에 굽지 않고 실행할 때마다 해석합니다. Homebrew로 설치했든 아카이브를
직접 풀었든 `go install`로 받았든 동작이 같습니다. 바이너리는 자신을 실행한 사용자의
환경을 보고, 그 사용자의 홈 디렉터리를 씁니다.

해석 순서는 AWS CLI와 동일합니다. 그래야 `aws` 명령이 보는 것과 cloudloupe가 보는 것이
어긋나지 않습니다.

| 대상 | 우선순위 |
| --- | --- |
| config 파일 | `AWS_CONFIG_FILE` → `~/.aws/config` |
| credentials 파일 | `AWS_SHARED_CREDENTIALS_FILE` → `~/.aws/credentials` |
| 기본 프로필 | `AWS_PROFILE` → `AWS_DEFAULT_PROFILE` |
| 기본 리전 | `AWS_REGION` → `AWS_DEFAULT_REGION` |

Windows에서는 홈 디렉터리를 `%USERPROFILE%`에서 찾고, 비어 있으면
`%HOMEDRIVE%%HOMEPATH%`로 넘어갑니다.

출력 맨 아래에 실제로 읽은 파일 경로와 그 경로가 결정된 근거를 항상 표시합니다.
"내 프로필이 왜 안 보이냐"의 답은 거의 항상 예상과 다른 파일을 읽고 있는 것이고, 원인은
대개 환경 변수입니다.

```
설정 위치
  config       /Users/you/.aws/config (홈 디렉터리, 있음)
  credentials  /Users/you/.aws/credentials (홈 디렉터리, 있음)
```

### `--check` 로 검증하기

경로를 보여주는 것과 그 경로가 실제로 쓸 수 있는지 확인하는 것은 다른 일입니다.
`cloudloupe --check` 는 해석된 위치를 검증하고, 문제가 있으면 0이 아닌 코드로 끝납니다.
스크립트에서 사전 점검으로 쓸 수 있습니다.

```
상태  항목                       내용
정상  홈 디렉터리                /Users/you
정상  설정 디렉터리              /Users/you/.aws
정상  config 파일                /Users/you/.aws/config (홈 디렉터리, 있음)
정상  credentials 파일           /Users/you/.aws/credentials (홈 디렉터리, 있음)
주의  기본 프로필 (AWS_PROFILE)  "pmdb" 프로필이 설정에 없습니다
정상  다른 위치의 설정           없음

프로필 10개를 읽었습니다.
aws 명령과 대조: 일치 (프로필 10개)
```

같은 사용자, 같은 기계에서도 cloudloupe가 보는 파일과 `aws` 명령이 보는 파일이 갈릴 수
있습니다. `--check` 가 잡아내는 것들입니다.

- **`aws` 명령과 프로필 목록 대조.** 두 목록이 다르면 서로 다른 설정을 읽고 있다는 뜻입니다.
  같은 파일을 보고 있는지에 대한 결정적인 답입니다.
- **snap으로 설치한 AWS CLI.** Linux에서 snap은 격리된 홈(`~/snap/aws-cli/current/.aws`)에
  설정을 만듭니다. `aws configure` 를 실행했는데 `~/.aws` 는 비어 있는 상황이 됩니다.
- **sudo 실행.** 홈이 root의 것으로 바뀌어 프로필이 사라진 것처럼 보입니다. cloudloupe는
  조회 전용이라 sudo가 필요하지 않습니다.
- **읽을 수 없는 파일.** 파일이 있는 것과 읽을 수 있는 것은 다릅니다. 이건 주의가 아니라
  문제로 보고합니다.
- **끊어진 심볼릭 링크.** dotfile 관리 도구를 쓸 때 생깁니다.
- **`AWS_PROFILE` 이 존재하지 않는 프로필을 가리키는 경우.** 오타나 이미 지운 프로필입니다.
- **자격증명 파일 권한이 열려 있는 경우.** AWS CLI는 검사하지 않지만, 장기 액세스 키가 같은
  기계의 다른 사용자에게 읽힌다면 알려줄 가치가 있습니다.

## 필요한 IAM 권한

cloudloupe는 조사하는 서비스에 대한 읽기 권한이 필요합니다. AWS 관리형 `ReadOnlyAccess`
정책이면 충분하고도 남습니다. 최소 권한을 선호하면
[`examples/iam/cloudloupe-readonly-policy.json`](examples/iam/cloudloupe-readonly-policy.json)의
범위를 좁힌 정책을 쓰세요.

권한이 없어도 치명적이지 않습니다. 읽을 수 없는 리전이나 리소스 타입은 화면에 그대로
보고되고 나머지는 정상적으로 불러옵니다. 여러 계정을 감사할 때는 부분 결과가 결과 없음보다
낫습니다.

## 로드맵

| 단계 | 범위                                                                              | 상태 |
| ---- | --------------------------------------------------------------------------------- | ---- |
| 1    | 프로필 선택, 호출자 신원 확인, 리전 선택, EC2 인스턴스 조회 TUI                    | 완료 |
| 2    | EBS, ENI, EIP, VPC, 서브넷, 보안 그룹, ALB/NLB, 타깃 그룹, Route 53, WAF 수집기 | 완료 |
| 3    | 관계 그래프 코어와 ALB → Listener → TG, Route 53 → ALB 식별자 연결                 | 진행 중 |
| 4    | 근거와 신뢰도를 갖춘 미사용 후보 탐지. CloudWatch와 CloudTrail을 결합              | 예정 |
| 5    | JSON / CSV / Markdown 리포트                                                         | 예정 |
| 6    | GoReleaser 기반 GitHub Release·체크섬과 설치 스크립트, Homebrew tap 연결               | 진행 중 |

판정 결과를 근거 없이 단정해서 내놓지 않습니다. 각 항목에는 **확정**, **추정**,
**확인 필요** 중 하나의 신뢰도와 그 판단에 사용한 API 응답 및 지표가 함께 붙습니다.
믿으라고 하는 대신 직접 판단할 수 있게 하려는 것입니다.

## 소스 구조

```text
cmd/cloudloupe          플래그, 입출력, TUI 실행
internal/app            프로필·리전별 AWS 설정과 수집 실행 조립
internal/catalog        타입 ID·표시명·범위·테이블 열·수집기 생성의 단일 출처
internal/collect        AWS SDK를 모르는 Collector/Registry/Runner 실행 코어
internal/collector/ec2  EC2 계열 조회와 SDK → model 변환
internal/collector/elbv2
internal/collector/route53
internal/collector/wafv2
internal/graph          ID·ARN·DNS 관계 해석과 정방향·역방향 인덱스
internal/model          외부 의존성이 없는 도메인 모델
internal/tui            Bubble Tea 상태 전이와 렌더링
```

새 리소스는 `internal/model`에 안정적인 타입 ID와 정렬 순서를 추가하고, 해당 서비스의
`internal/collector/<service>`에 좁은 조회 인터페이스·수집기·자격증명 없는 테스트 대역을
구현한 뒤 `internal/catalog`의 서비스 그룹에 정의를 등록합니다. README 지원 표와 IAM 예제
정책도 함께 확인합니다. 수집기가 표준 `Resource`와 `Ref`를 만들면 `internal/graph`가
ID·ARN·DNS 식별자를 공통 규칙으로 연결하며, 미해결되거나 모호한 관계도 버리지 않습니다.
`internal/collect`와 TUI 화면 전이 코드는 신규 리소스 때문에 수정하지 않습니다.

## 개발

처음 기여한다면 [CONTRIBUTING.md](CONTRIBUTING.md)의 개발 환경, 작업 순서, PR 전 검사
목록을 먼저 확인하세요. 버전 태그와 GitHub Release 운영은 [RELEASING.md](RELEASING.md)에
정리되어 있습니다.

```sh
make test       # 전체 단위 테스트
make ci         # gofmt, vet, 조회 전용 가드, 테스트, 빌드
make lint       # golangci-lint 실행
make test-race  # 데이터 레이스 검사
make cross      # 지원하는 6개 OS/아키텍처 조합 빌드
make snapshot   # 태그 없이 GoReleaser 릴리스 산출물 검사
make build      # 현재 플랫폼 바이너리 빌드
make help       # 전체 타깃과 설명
```

설계 규칙은 [`.kiro/steering/go-conventions.md`](.kiro/steering/go-conventions.md)에 있습니다.
조회 전용 강제는 타협 대상이 아닙니다.

## 라이선스

[MIT](LICENSE)
