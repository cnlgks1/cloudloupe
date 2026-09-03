package ecr_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ecrcollector "github.com/cnlgks1/cloudloupe/internal/collector/ecr"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeECR는 DescribeRepositories를 대신한다. 테스트는 실제 AWS를 때리지 않는다.
//
// pages는 페이지별 리포지토리 목록, pageErr는 마지막 페이지 뒤에 낼 오류다.
type fakeECR struct {
	pages   [][]ecrtypes.Repository
	pageErr error

	calls int
}

func (f *fakeECR) DescribeRepositories(
	_ context.Context,
	_ *awsecr.DescribeRepositoriesInput,
	_ ...func(*awsecr.Options),
) (*awsecr.DescribeRepositoriesOutput, error) {
	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awsecr.DescribeRepositoriesOutput{}, nil
	}

	out := &awsecr.DescribeRepositoriesOutput{Repositories: f.pages[i]}
	if i+1 < len(f.pages) || f.pageErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestRepositoryCollectorType(t *testing.T) {
	t.Parallel()

	if got := ecrcollector.NewRepository(&fakeECR{}).Type(); got != model.TypeECRRepository {
		t.Errorf("Type() = %q, want %q", got, model.TypeECRRepository)
	}
}

// TestRepositoryCollectConvertsFields는 SDK 값을 그대로 담고 KMS 암호화면 키 관계를 만드는지
// 확인한다.
func TestRepositoryCollectConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	arn := "arn:aws:ecr:ap-northeast-2:123456789012:repository/app"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"

	api := &fakeECR{
		pages: [][]ecrtypes.Repository{{
			{
				RepositoryName:     aws.String("app"),
				RepositoryArn:      aws.String(arn),
				RepositoryUri:      aws.String("123456789012.dkr.ecr.ap-northeast-2.amazonaws.com/app"),
				CreatedAt:          &created,
				ImageTagMutability: ecrtypes.ImageTagMutabilityImmutable,
				EncryptionConfiguration: &ecrtypes.EncryptionConfiguration{
					EncryptionType: ecrtypes.EncryptionTypeKms,
					KmsKey:         aws.String(kmsKey),
				},
			},
		}},
	}

	got, err := ecrcollector.NewRepository(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("리포지토리 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "app" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	// 값은 AWS가 준 그대로. IMMUTABLE/KMS를 번역하지 않는다.
	if got, want := res.FieldValue("ImageTagMutability"), "IMMUTABLE"; got != want {
		t.Errorf("ImageTagMutability = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("EncryptionType"), "KMS"; got != want {
		t.Errorf("EncryptionType = %q, want %q", got, want)
	}

	if len(res.Related) != 1 {
		t.Fatalf("관계 %d개, want 1", len(res.Related))
	}
	ref := res.Related[0]
	if ref.Type != model.TypeKMSKey || ref.Relation != "EncryptionConfiguration.KmsKey" || ref.ID != kmsKey {
		t.Errorf("관계 = %+v", ref)
	}
	if ref.IdentifierKind != model.IdentifierARN {
		t.Errorf("IdentifierKind = %q, want %q", ref.IdentifierKind, model.IdentifierARN)
	}
}

// TestRepositoryCollectAES256HasNoKeyRelation은 AES256 암호화면 KMS 키가 없어 관계를 만들지
// 않는지 확인한다.
func TestRepositoryCollectAES256HasNoKeyRelation(t *testing.T) {
	t.Parallel()

	api := &fakeECR{
		pages: [][]ecrtypes.Repository{{
			{
				RepositoryName: aws.String("app"),
				EncryptionConfiguration: &ecrtypes.EncryptionConfiguration{
					EncryptionType: ecrtypes.EncryptionTypeAes256,
				},
			},
		}},
	}

	got, err := ecrcollector.NewRepository(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if got[0].FieldValue("EncryptionType") != "AES256" {
		t.Errorf("EncryptionType = %q, want AES256", got[0].FieldValue("EncryptionType"))
	}
	if len(got[0].Related) != 0 {
		t.Errorf("Related = %+v, want 없음", got[0].Related)
	}
	if got[0].FieldValue("KmsKey") != "-" {
		t.Errorf("KmsKey = %q, want -", got[0].FieldValue("KmsKey"))
	}
}

// TestRepositoryCollectFollowsPages는 페이지네이션을 이어 받는지 확인한다.
func TestRepositoryCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeECR{
		pages: [][]ecrtypes.Repository{
			{{RepositoryName: aws.String("a")}},
			{{RepositoryName: aws.String("b")}},
		},
	}

	got, err := ecrcollector.NewRepository(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a", "b"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
	if api.calls != 2 {
		t.Errorf("DescribeRepositories 호출 = %d회, want 2", api.calls)
	}
}

// TestRepositoryCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestRepositoryCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeECR{
		pages:   [][]ecrtypes.Repository{{{RepositoryName: aws.String("a")}}},
		pageErr: denied,
	}

	got, err := ecrcollector.NewRepository(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
