package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	acmcollector "github.com/cnlgks1/cloudloupe/internal/collector/acm"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func acmDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsacm.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsacm.Client {
		clientOnce.Do(func() {
			client = awsacm.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeACMCertificate,
			Label:   "Certificates",
			Scope:   Regional,
			Columns: []string{"Status", "Type", "KeyAlgorithm", "SubjectAlternativeNames", "NotBefore", "NotAfter", "RenewalEligibility", "Issuer", "InUseBy"},
			newCollector: func() collect.Collector {
				return acmcollector.NewCertificate(clientFor())
			},
		},
	}
}
