package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsapigw "github.com/aws/aws-sdk-go-v2/service/apigateway"
	awsapigwv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	apigwcollector "github.com/cnlgks1/cloudloupe/internal/collector/apigateway"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func apigatewayDefinitions(cfg aws.Config) []Definition {
	var (
		restClient     *awsapigw.Client
		restClientOnce sync.Once
		v2Client       *awsapigwv2.Client
		v2ClientOnce   sync.Once
	)
	restClientFor := func() *awsapigw.Client {
		restClientOnce.Do(func() {
			restClient = awsapigw.NewFromConfig(cfg)
		})

		return restClient
	}
	v2ClientFor := func() *awsapigwv2.Client {
		v2ClientOnce.Do(func() {
			v2Client = awsapigwv2.NewFromConfig(cfg)
		})

		return v2Client
	}

	return []Definition{
		{
			Type:           model.TypeAPIGatewayRestAPI,
			Label:          "REST APIs",
			Scope:          Regional,
			Columns:        []string{"Id", "Description", "Version", "EndpointConfiguration.Types", "ApiKeySource"},
			SummaryColumns: []string{"Id", "EndpointConfiguration.Types"},
			newCollector: func() collect.Collector {
				return apigwcollector.NewRestAPI(restClientFor())
			},
		},
		{
			Type:           model.TypeAPIGatewayV2API,
			Label:          "HTTP and WebSocket APIs",
			Scope:          Regional,
			Columns:        []string{"ApiId", "ProtocolType", "ApiEndpoint", "Version", "RouteSelectionExpression", "DisableExecuteApiEndpoint"},
			SummaryColumns: []string{"ApiId", "ProtocolType"},
			newCollector: func() collect.Collector {
				return apigwcollector.NewV2API(v2ClientFor())
			},
		},
	}
}
