package secretsmanager_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	smcollector "github.com/cnlgks1/cloudloupe/internal/collector/secretsmanager"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeSM은 시크릿 수집기가 쓰는 ListSecrets를 대신한다.
//
// pages는 ListSecrets의 페이지들, pageErr는 마지막 페이지 뒤에 낼 오류다.
type fakeSM struct {
	pages   [][]smtypes.SecretListEntry
	pageErr error

	calls int
}

func (f *fakeSM) ListSecrets(
	_ context.Context,
	_ *awssm.ListSecretsInput,
	_ ...func(*awssm.Options),
) (*awssm.ListSecretsOutput, error) {
	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awssm.ListSecretsOutput{}, nil
	}

	out := &awssm.ListSecretsOutput{SecretList: f.pages[i]}
	if i+1 < len(f.pages) || f.pageErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestSecretCollectorType(t *testing.T) {
	t.Parallel()

	if got := smcollector.NewSecret(&fakeSM{}).Type(); got != model.TypeSecretsManagerSecret {
		t.Errorf("Type() = %q, want %q", got, model.TypeSecretsManagerSecret)
	}
}

// TestSecretCollectConvertsFieldsAndRelations는 메타데이터 값을 그대로 담고 KMS 키·로테이션
// Lambda 관계를 만드는지 확인한다.
func TestSecretCollectConvertsFieldsAndRelations(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	changed := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	arn := "arn:aws:secretsmanager:ap-northeast-2:123456789012:secret:prod/db-abc"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"
	rotationLambda := "arn:aws:lambda:ap-northeast-2:123456789012:function:rotate-db"

	api := &fakeSM{
		pages: [][]smtypes.SecretListEntry{{
			{
				Name:              aws.String("prod/db"),
				ARN:               aws.String(arn),
				Description:       aws.String("DB 자격증명"),
				KmsKeyId:          aws.String(kmsKey),
				RotationEnabled:   aws.Bool(true),
				RotationLambdaARN: aws.String(rotationLambda),
				CreatedDate:       &created,
				LastChangedDate:   &changed,
			},
		}},
	}

	got, err := smcollector.NewSecret(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("시크릿 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "prod/db" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	if got, want := res.FieldValue("RotationEnabled"), "true"; got != want {
		t.Errorf("RotationEnabled = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("LastChangedDate"), "2025-06-01T00:00:00Z"; got != want {
		t.Errorf("LastChangedDate = %q, want %q", got, want)
	}

	type rel struct {
		relation string
		typ      string
		id       string
	}
	gotRels := make([]rel, 0, len(res.Related))
	for _, r := range res.Related {
		gotRels = append(gotRels, rel{r.Relation, r.Type, r.ID})
	}
	want := []rel{
		{"KmsKeyId", model.TypeKMSKey, kmsKey},
		{"RotationLambdaARN", model.TypeLambdaFunction, rotationLambda},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 = %+v, want %+v", gotRels, want)
	}
}

// TestSecretCollectDistinguishesMissing은 로테이션 없고 기본 암호화면 관계가 없고 시각이
// "-"로 구분되는지 확인한다.
func TestSecretCollectDistinguishesMissing(t *testing.T) {
	t.Parallel()

	api := &fakeSM{
		pages: [][]smtypes.SecretListEntry{{
			{Name: aws.String("plain"), ARN: aws.String("arn:x")},
		}},
	}

	got, err := smcollector.NewSecret(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got[0].Related) != 0 {
		t.Errorf("KMS·로테이션 없으면 관계 없음, got %+v", got[0].Related)
	}
	if v := got[0].FieldValue("RotationEnabled"); v != "-" {
		t.Errorf("RotationEnabled 없음 = %q, want -", v)
	}
	if v := got[0].FieldValue("LastAccessedDate"); v != "-" {
		t.Errorf("LastAccessedDate 없음 = %q, want -", v)
	}
}

// TestSecretCollectFollowsPages는 페이지네이션을 이어 받는지 확인한다.
func TestSecretCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeSM{
		pages: [][]smtypes.SecretListEntry{
			{{Name: aws.String("a"), ARN: aws.String("arn:a")}},
			{{Name: aws.String("b"), ARN: aws.String("arn:b")}},
		},
	}

	got, err := smcollector.NewSecret(api).Collect(context.Background(), collect.Request{Scope: testScope()})
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
		t.Errorf("ListSecrets 호출 = %d회, want 2", api.calls)
	}
}

// TestSecretCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestSecretCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeSM{
		pages:   [][]smtypes.SecretListEntry{{{Name: aws.String("a"), ARN: aws.String("arn:a")}}},
		pageErr: denied,
	}

	got, err := smcollector.NewSecret(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
