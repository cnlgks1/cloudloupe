# 예제

cloudloupe를 설정하고 출력을 읽는 데 참고할 자료입니다. 프로그램이 이 파일들을 읽어들이지는
않습니다. 필요한 부분을 복사해 쓰시면 됩니다.

여기 나오는 계정 ID, ARN, IP 주소, 키는 모두 가짜입니다.

```
examples/
├── iam/       cloudloupe에 조회 권한을 주는 IAM 정책
├── aws/       주석을 붙인 ~/.aws/config, ~/.aws/credentials 샘플
└── reports/   --output json / csv / md 의 실제 출력 형태
```

## `iam/`

### `cloudloupe-readonly-policy.json`

cloudloupe가 조사하는 모든 대상을 포괄하는 최소 권한 정책입니다. cloudloupe가 사용할 사용자나
역할에 붙이면 됩니다.

개별 액션을 나열하는 대신 `ec2:Describe*`, `route53:Get*` 같은 **접두사 allow-list**로
작성했습니다. 코드에서 조회 전용을 강제하는 방식과 같은 규칙을 쓰게 해서 둘이 어긋나지
않도록 한 것입니다. 조회가 아닌 호출은 IAM 권한도 없고 그것을 수행할 코드 경로도 없습니다.

짚어둘 만한 두 가지가 있습니다.

- `cloudtrail:LookupEvents`는 `Describe`, `List`, `Get`으로 시작하지 않습니다. allow-list에
  `Lookup`이 필요한 실제 사례가 이것입니다. 이런 추가는 allow-list에 명시적으로 넣어야 하고,
  포괄적인 예외로 처리해서는 안 됩니다.
- 마지막 statement는 `ec2:GetPasswordData`, `ec2:GetConsoleOutput`,
  `ec2:GetConsoleScreenshot`을 **명시적으로 Deny**합니다. 기술적으로는 조회지만 인스턴스
  내용을 노출하고 cloudloupe가 호출할 이유가 없습니다. 이 자격증명이 다른 곳에서 오용될 때의
  피해 범위를 줄입니다.

별도 정책을 관리하고 싶지 않다면 AWS 관리형 `ReadOnlyAccess`로도 충분합니다.
`ViewOnlyAccess`는 더 좁아서 인벤토리 조회에는 동작하지만, 4단계 판정이 근거로 쓰는
CloudWatch·CloudTrail 조회 일부가 빠집니다.

### `cross-account-role-trust-policy.json`

하나의 신원으로 여러 계정을 조사할 때 쓰는 신뢰 정책입니다. 각 대상 계정에
`cloudloupe-readonly` 역할을 만들고 위의 신원 정책을 붙인 다음, 이 파일을 그 역할의 신뢰
정책으로 설정하고, `~/.aws/config`에 계정별로 `role_arn` 프로필을 하나씩 추가합니다.

MFA와 external ID를 요구합니다. external ID는 무작위로 만들어서 신뢰 정책과 프로필 설정에
같은 값을 쓰세요.

## `aws/`

`config.example`과 `credentials.example`은 cloudloupe가 실제로 탐색하는 프로필 형태를
보여줍니다. 정적 키, IAM Identity Center(SSO) 세션, 계정 간 assume-role 체인, 그리고 기본
리전이 없는 프로필까지 포함해서 리전 선택이 어떻게 동작하는지 볼 수 있게 했습니다.

cloudloupe는 프로필 이름과 기본 리전을 파악하기 위해 이 파일들을 **읽습니다.** 쓰지 않습니다.
비밀값은 AWS SDK 자격증명 체인이 해석하며, 메모리에만 있고 스냅샷 캐시와 리포트에서
제외됩니다.

장기 액세스 키보다 SSO나 assume-role을 권장합니다. SSO를 쓴다면 먼저 로그인하세요.
cloudloupe는 캐시된 토큰을 사용하되 로그인 절차 자체는 수행하지 않습니다.

```sh
aws sso login --profile prod
```

## `reports/`

작은 가상 환경의 출력 샘플입니다. 인터넷에 노출된 ALB가 정상 상태의 인스턴스 두 대를 가진
타깃 그룹으로 전달하고, 여기에 고아 자원 세 개가 붙어 있습니다. 연결되지 않은 EBS 볼륨,
분리된 ENI, 연결되지 않은 Elastic IP입니다.

| 파일 | 생성 명령 |
| --- | --- |
| `resources.json` | `cloudloupe --output json` |
| `resources.csv` | `cloudloupe --output csv` |
| `report.md` | `cloudloupe --output md` |

이 샘플들이 보여주려는 것은 세 가지입니다.

**관계는 양쪽 끝에 기록됩니다.** 타깃 그룹은 자신이 대상으로 삼는 인스턴스를 나열하고, 각
인스턴스는 자신이 속한 타깃 그룹을 나열합니다. 3단계 그래프는 이 ref로부터 만들어지므로,
ref를 빼먹은 수집기는 연결이 끊긴 노드를 만들어냅니다.

**필드 순서는 고정입니다.** 필드는 map이 아니라 순서 있는 목록이고, 그래서 JSON이
`[{"key": ..., "value": ...}]` 형태입니다. Go는 map 순회 순서를 무작위화하므로, map을 쓰면
상세 뷰가 렌더링마다 뒤섞이고 스냅샷 사이의 diff가 무의미해집니다.

**에러는 데이터를 대체하지 않고 함께 실립니다.** 이 실행은 `eu-west-1`의 WAF에서
`AccessDeniedException`을 만났습니다. 그래도 리포트에는 읽어낼 수 있었던 모든 리소스가 담겨
있고, 실패는 AWS 에러 코드와 함께 사람이 읽는 설명으로 기록됩니다. 한 리전의 권한 누락이
멀티 리전 수집의 나머지를 버리는 일은 없습니다.

**JSON 키와 리소스 타입 ID는 영어입니다.** 주석과 문서는 한국어로 쓰지만 `accountId`,
`ec2:instance`, `attached-eni` 같은 값은 출력 계약이라서 번역하지 않습니다. 번역하면 이
출력을 파싱하는 스크립트와 저장된 스냅샷이 모두 깨집니다.
