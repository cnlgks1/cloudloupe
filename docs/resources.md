# 지원 리소스

모든 조회는 `Describe`/`List`/`Get` 계열 API만 사용합니다. 지원 범위는 릴리스마다 늘어납니다.

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
| ElastiCache | `elasticache:replicationGroup` | `elasticache.DescribeReplicationGroups` |
| ElastiCache | `elasticache:cacheCluster` | `elasticache.DescribeCacheClusters` |
| SNS | `sns:topic` | `sns.ListTopics`, `GetTopicAttributes` |
| SQS | `sqs:queue` | `sqs.ListQueues`, `GetQueueAttributes` |
| EventBridge | `events:eventBus` | `eventbridge.ListEventBuses` |
| EventBridge | `events:rule` | `eventbridge.ListEventBuses`, `ListRules` |
| API Gateway | `apigateway:restApi` | `apigateway.GetRestApis` |
| API Gateway | `apigatewayv2:api` | `apigatewayv2.GetApis` |
| Secrets Manager | `secretsmanager:secret` | `secretsmanager.ListSecrets` |
| SSM Parameter Store | `ssm:parameter` | `ssm.DescribeParameters` |
| ACM | `acm:certificate` | `acm.ListCertificates`, `DescribeCertificate` |
| Route 53 | `route53:recordSet` | `route53.ListHostedZones`, `ListResourceRecordSets` |
| WAF | `wafv2:webAcl` | `wafv2.ListWebACLs`, `GetWebACL` (REGIONAL 스코프) |
| IAM | `iam:role` | `iam.ListRoles` |
| IAM | `iam:user` | `iam.ListUsers` |
| IAM | `iam:group` | `iam.ListGroups` |
| IAM | `iam:policy` | `iam.ListPolicies` (Local 스코프) |
| KMS | `kms:key` | `kms.ListKeys`, `DescribeKey`, `ListAliases` |
| S3 | `s3:bucket` | `s3.ListBuckets` |
| CloudFront | `cloudfront:distribution` | `cloudfront.ListDistributions` |

## 리전 스코프

Route 53과 IAM, CloudFront는 글로벌 서비스라 리전 선택과 무관하게 한 번만 조회하고 리전이
`global`로 표시됩니다. 나머지는 선택한 리전마다 조회합니다.

## 민감한 값은 조회하지 않는다

Secrets Manager 시크릿과 SSM 파라미터는 메타데이터만 조회합니다. 시크릿 값이나 파라미터
값을 읽는 `GetSecretValue`·`GetParameter`는 호출하지 않습니다. 조회 전용 경계를 지키고
민감한 값이 화면이나 로그에 노출되지 않게 하려는 것입니다.

## 필드와 값 표기

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

## 관계

상세 화면(목록에서 `enter`)은 관계를 함께 보여줍니다. 관계 이름은 그 연결을 만든 SDK 응답
필드 경로(`DBClusterIdentifier`, `VpcConfig.SubnetIds`, `Routes.NatGatewayId`)라 `aws` CLI
출력과 대조할 수 있습니다. 대상은 타입과 이름으로 표시합니다. 대상 타입을 같이 조회하지
않았으면 이름을 알 수 없으므로 ID만 보여주며, 그 타입도 함께 조회하면 이름이 채워집니다.
`Referenced by`는 그 리소스를 가리키는 다른 리소스입니다. AWS에는 역방향을 알려주는 API가
없어 추가 호출 없이 조회 결과에서 계산합니다.
