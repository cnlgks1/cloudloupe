module github.com/cnlgks1/cloudloupe

// 하한은 현재 의존성 중 가장 높은 요구치를 따른다. 검증한 값들:
//
//	golang.org/x/sys                 1.25.0  ← 가장 높음
//	charmbracelet/bubbles           1.24.2
//	charmbracelet/bubbletea         1.24.0
//	aws-sdk-go-v2                   1.24
//	charmbracelet/lipgloss          1.18
//
// cloudloupe 자체 코드는 1.22면 충분하다. 하한이 높은 것은 전부 의존성 때문이다.
//
// 로컬 툴체인의 정확한 패치 버전으로 고정하지 않는다. 그러면 모든 기여자가 그 빌드를
// 써야 한다. GOTOOLCHAIN=auto가 기본이므로, 이보다 낮은 Go를 쓰는 사람은 필요한 툴체인을
// 자동으로 받아온다.
go 1.25.0

require (
	github.com/aws/aws-sdk-go-v2 v1.45.1
	github.com/aws/aws-sdk-go-v2/config v1.33.1
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.45.0
	github.com/aws/aws-sdk-go-v2/service/apigatewayv2 v1.40.0
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.76.0
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.66.0
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.325.1
	github.com/aws/aws-sdk-go-v2/service/ecr v1.63.0
	github.com/aws/aws-sdk-go-v2/service/ecs v1.94.0
	github.com/aws/aws-sdk-go-v2/service/eks v1.96.0
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.60.1
	github.com/aws/aws-sdk-go-v2/service/iam v1.47.0
	github.com/aws/aws-sdk-go-v2/service/kms v1.47.0
	github.com/aws/aws-sdk-go-v2/service/lambda v1.106.0
	github.com/aws/aws-sdk-go-v2/service/rds v1.127.0
	github.com/aws/aws-sdk-go-v2/service/route53 v1.67.1
	github.com/aws/aws-sdk-go-v2/service/s3 v1.93.0
	github.com/aws/aws-sdk-go-v2/service/sns v1.45.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.50.0
	github.com/aws/aws-sdk-go-v2/service/sts v1.47.1
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.80.1
	github.com/aws/smithy-go v1.28.1
	github.com/charmbracelet/bubbles v1.0.0
	github.com/charmbracelet/bubbletea v1.3.10
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/mattn/go-isatty v0.0.24
)

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.20.1 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.19.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.7.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.35.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.40.1 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.1 // indirect
	github.com/charmbracelet/x/ansi v0.11.6 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.9.0 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.5.0 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.3.8 // indirect
)
