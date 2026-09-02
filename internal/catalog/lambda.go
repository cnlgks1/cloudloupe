package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	lambdacollector "github.com/cnlgks1/cloudloupe/internal/collector/lambda"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// lambdaDefinitions는 Lambda 그룹의 리소스 타입을 조립한다.
func lambdaDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awslambda.Client
		clientOnce sync.Once
	)
	clientFor := func() *awslambda.Client {
		clientOnce.Do(func() {
			client = awslambda.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeLambdaFunction,
			Label:          "Functions",
			Scope:          Regional,
			Columns:        []string{"Runtime", "PackageType", "Architectures", "MemorySize", "Timeout", "LastModified", "Role"},
			SummaryColumns: []string{"Runtime", "PackageType", "MemorySize"},
			newCollector: func() collect.Collector {
				return lambdacollector.NewFunction(clientFor())
			},
		},
	}
}
