<h1 align="center">cloudloupe</h1>

<p align="center">
  <em>A read-only AWS infrastructure investigation TUI — inspect resources, relationships, and evidence across profiles.</em>
</p>

<p align="center">
  <a href="https://github.com/cnlgks1/cloudloupe/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/cnlgks1/cloudloupe/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://goreportcard.com/report/github.com/cnlgks1/cloudloupe"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/cnlgks1/cloudloupe"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go version" src="https://img.shields.io/badge/go-1.24.2%2B-00ADD8.svg">
</p>

---

> **개발 초기 단계입니다.** 프로젝트 구조와 규약, 릴리스 파이프라인은 자리를 잡았고
> 리소스 수집과 TUI를 만들고 있습니다. 지금 동작하는 범위는 [로드맵](#로드맵)을
>참고하세요.

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
- **로컬에 쓰는 것은** 스냅샷 캐시와 사용자가 요청한 리포트 파일뿐입니다.

필요한 IAM 권한은 읽기 권한뿐입니다. 최소 권한 정책은 [`examples/iam/`](examples/iam/)에
있습니다.

## 설치

아직 릴리스가 없습니다. 지금은 소스에서 빌드하는 방법만 동작합니다.

```sh
git clone https://github.com/cnlgks1/cloudloupe.git
cd cloudloupe
make build
./cloudloupe
```

첫 릴리스 이후에는 아래 방법이 추가됩니다. 태그를 붙이기 전까지는 동작하지 않습니다.

```sh
brew install cnlgks1/tap/cloudloupe                                # 릴리스 후
go install github.com/cnlgks1/cloudloupe/cmd/cloudloupe@latest     # 태그 후
```

저장소가 비공개인 동안에는 `go install`이 모듈 프록시를 통과하지 못합니다. 프록시를 우회하고
git 인증을 쓰도록 알려줘야 합니다.

```sh
export GOPRIVATE=github.com/cnlgks1/*
go install github.com/cnlgks1/cloudloupe/cmd/cloudloupe@latest
```

## 지원 플랫폼

단일 정적 바이너리이며, CGO도 런타임 의존성도 없습니다.

| OS      | amd64 | arm64 |
| ------- | :---: | :---: |
| macOS   |   O   |   O   |
| Linux   |   O   |   O   |
| Windows |   O   |   O   |

유니코드 박스 문자를 렌더링하지 못하는 Windows 터미널을 위해 ASCII 폴백이 있습니다.
자동으로 감지하며, `--ascii`나 `CLOUDLOUPE_ASCII=1`로 강제할 수도 있습니다.

## 사용법

지금 동작하는 것은 프로필 탐색뿐입니다. AWS에 접속하지 않으므로 자격증명 없이도 실행됩니다.

```sh
# 공유 설정에서 발견한 프로필 목록
cloudloupe

# 스크립트에서 쓸 JSON
cloudloupe --output json

# 설정 위치 진단
cloudloupe --check

# 버전 정보
cloudloupe --version
```

리전 선택, 리소스 조회, TUI, `--profile`/`--region`/`--ascii` 플래그, CSV·Markdown 리포트는
아직 없습니다. [로드맵](#로드맵)에서 어느 단계에 들어오는지 확인하세요.

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

| 단계 | 범위                                                                              | 상태     |
| ---- | --------------------------------------------------------------------------------- | -------- |
| 1    | 프로필 탐색, 호출자 신원 확인, 리전 선택, EC2 + ALB 조회 TUI                       | 진행 중  |
| 2    | EBS, ENI, EIP, 타깃 그룹(+타깃 상태), Route 53, WAF(REGIONAL)                      | 예정     |
| 3    | 관계 그래프: ALB → Listener → TG → EC2, Route 53 → ALB, EC2 → ENI/EIP/EBS          | 예정     |
| 4    | 근거와 신뢰도를 갖춘 미사용 후보 탐지. CloudWatch와 CloudTrail을 결합              | 예정     |
| 5    | JSON / CSV / Markdown 리포트, SQLite 스냅샷과 날짜별 diff                          | 예정     |
| 6    | GoReleaser 릴리스 파이프라인, Homebrew tap, 체크섬                                 | 뼈대만   |

판정 결과를 근거 없이 단정해서 내놓지 않습니다. 각 항목에는 **확정**, **추정**,
**확인 필요** 중 하나의 신뢰도와 그 판단에 사용한 API 응답 및 지표가 함께 붙습니다.
믿으라고 하는 대신 직접 판단할 수 있게 하려는 것입니다.

## 기여

이슈와 PR을 환영합니다. 개발 환경 설정은 [CONTRIBUTING.md](CONTRIBUTING.md)를 보시고,
이 코드베이스가 따르는 설계 규칙은
[`.kiro/steering/go-conventions.md`](.kiro/steering/go-conventions.md)에 정리되어 있습니다.
특히 조회 전용 강제는 타협 대상이 아닙니다.

## 보안

cloudloupe는 AWS 자격증명과 인프라 메타데이터를 다룹니다. 취약점 신고는
[SECURITY.md](SECURITY.md)를 참고하세요. 보안 문제는 공개 이슈로 열지 말아주세요.

## 라이선스

[MIT](LICENSE)

## 만든 것들

[Charm](https://charm.sh)의 [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles),
[Lip Gloss](https://github.com/charmbracelet/lipgloss)와
[AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2)로 만들었습니다.
