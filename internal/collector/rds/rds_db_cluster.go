package rds

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeDBClustersAPI는 DB 클러스터 수집에 필요한 SDK 메서드만 담는다.
type describeDBClustersAPI interface {
	DescribeDBClusters(context.Context, *awsrds.DescribeDBClustersInput, ...func(*awsrds.Options)) (*awsrds.DescribeDBClustersOutput, error)
}

// dbClusterCollector는 RDS DB 클러스터를 조회한다.
type dbClusterCollector struct {
	api describeDBClustersAPI
}

// NewDBCluster는 RDS DB 클러스터 수집기를 만든다.
func NewDBCluster(api describeDBClustersAPI) collect.Collector {
	return dbClusterCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c dbClusterCollector) Type() string { return model.TypeRDSDBCluster }

// Collect는 범위 안의 RDS DB 클러스터를 모두 조회해 도메인 리소스로 변환한다.
//
// SDK paginator가 중간 페이지에서 실패하면 앞 페이지에서 변환한 리소스는 보존한다.
func (c dbClusterCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsrds.NewDescribeDBClustersPaginator(c.api, &awsrds.DescribeDBClustersInput{})

	var resources []model.Resource
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return resources, fmt.Errorf("describe DB clusters: %w", err)
		}

		for i := range page.DBClusters {
			resources = append(resources, dbClusterToResource(req.Scope, page.DBClusters[i]))
		}
	}

	return resources, nil
}

// dbClusterToResource는 SDK DB 클러스터의 포인터 필드를 도메인 값으로 변환한다.
func dbClusterToResource(scope collect.Scope, cluster rdstypes.DBCluster) model.Resource {
	identifier := aws.ToString(cluster.DBClusterIdentifier)

	resource := model.Resource{
		Type:      model.TypeRDSDBCluster,
		ID:        identifier,
		Name:      identifier,
		ARN:       aws.ToString(cluster.DBClusterArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(cluster.Status),
		CreatedAt: utcTime(cluster.ClusterCreateTime),
		Fields: []model.Field{
			{Key: "Engine", Value: orDash(aws.ToString(cluster.Engine))},
			{Key: "EngineMode", Value: orDash(aws.ToString(cluster.EngineMode))},
			{Key: "EngineVersion", Value: orDash(aws.ToString(cluster.EngineVersion))},
			{Key: "DatabaseName", Value: orDash(aws.ToString(cluster.DatabaseName))},
			{Key: "Endpoint", Value: orDash(aws.ToString(cluster.Endpoint))},
			{Key: "ReaderEndpoint", Value: orDash(aws.ToString(cluster.ReaderEndpoint))},
			{Key: "Port", Value: int32Value(cluster.Port)},
			{Key: "MultiAZ", Value: boolValue(cluster.MultiAZ)},
			{Key: "DBClusterInstanceClass", Value: orDash(aws.ToString(cluster.DBClusterInstanceClass))},
			{Key: "StorageType", Value: orDash(aws.ToString(cluster.StorageType))},
			{Key: "AllocatedStorage", Value: int32Value(cluster.AllocatedStorage)},
			{Key: "StorageEncrypted", Value: boolValue(cluster.StorageEncrypted)},
			{Key: "KmsKeyId", Value: orDash(aws.ToString(cluster.KmsKeyId))},
			{Key: "BackupRetentionPeriod", Value: int32Value(cluster.BackupRetentionPeriod)},
			{Key: "DBSubnetGroup", Value: orDash(aws.ToString(cluster.DBSubnetGroup))},
			{Key: "AvailabilityZones", Value: stringListValue(cluster.AvailabilityZones)},
			{Key: "DBClusterParameterGroup", Value: orDash(aws.ToString(cluster.DBClusterParameterGroup))},
			{Key: "PreferredBackupWindow", Value: orDash(aws.ToString(cluster.PreferredBackupWindow))},
			{Key: "PreferredMaintenanceWindow", Value: orDash(aws.ToString(cluster.PreferredMaintenanceWindow))},
			{Key: "IAMDatabaseAuthenticationEnabled", Value: boolValue(cluster.IAMDatabaseAuthenticationEnabled)},
			{Key: "DeletionProtection", Value: boolValue(cluster.DeletionProtection)},
		},
		Tags: rdsTags(cluster.TagList),
		Identifiers: dnsIdentifiers(
			aws.ToString(cluster.Endpoint),
			aws.ToString(cluster.ReaderEndpoint),
		),
	}

	for _, member := range cluster.DBClusterMembers {
		resource.Related = appendIDRelation(
			resource.Related,
			model.TypeRDSDBInstance,
			"DBClusterMembers.DBInstanceIdentifier",
			aws.ToString(member.DBInstanceIdentifier),
		)
	}
	resource.Related = appendKMSRelation(resource.Related, aws.ToString(cluster.KmsKeyId))
	for _, group := range cluster.VpcSecurityGroups {
		resource.Related = appendIDRelation(
			resource.Related,
			model.TypeEC2SecurityGroup,
			"VpcSecurityGroups.VpcSecurityGroupId",
			aws.ToString(group.VpcSecurityGroupId),
		)
	}

	return resource
}
