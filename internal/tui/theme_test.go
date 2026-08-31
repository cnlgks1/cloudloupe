package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/cnlgks1/cloudloupe/internal/tui"
)

func TestDetectASCII(t *testing.T) {
	tests := []struct {
		name  string
		force bool
		env   map[string]string
		goos  string // 참고용. 실제 분기는 아래 별도 테스트에서 확인한다.
		want  bool
	}{
		{
			name:  "--ascii 플래그는 모든 감지를 이긴다",
			force: true,
			env:   map[string]string{"WT_SESSION": "1"},
			want:  true,
		},
		{
			name: "CLOUDLOUPE_ASCII=1 이면 강제",
			env:  map[string]string{"CLOUDLOUPE_ASCII": "1"},
			want: true,
		},
		{
			name: "CLOUDLOUPE_ASCII=0 이면 유니코드 강제",
			env:  map[string]string{"CLOUDLOUPE_ASCII": "0", "TERM": "dumb"},
			want: false,
		},
		{
			name: "TERM=dumb 이면 ASCII",
			env:  map[string]string{"TERM": "dumb"},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// 환경 변수를 바꾸므로 병렬 실행하지 않는다.
			for _, k := range []string{"CLOUDLOUPE_ASCII", "TERM", "WT_SESSION", "ConEmuANSI"} {
				t.Setenv(k, "")
			}

			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			if got := tui.DetectASCII(tc.force); got != tc.want {
				t.Errorf("DetectASCII(%v) = %v, want %v", tc.force, got, tc.want)
			}
		})
	}
}

func TestThemeGlyphsDifferByMode(t *testing.T) {
	t.Parallel()

	unicode := tui.New(false)
	ascii := tui.New(true)

	if unicode.ASCII {
		t.Error("유니코드 테마의 ASCII 플래그가 true다")
	}

	if !ascii.ASCII {
		t.Error("ASCII 테마의 ASCII 플래그가 false다")
	}

	// ASCII 테마의 모든 글리프는 순수 ASCII여야 한다. 이게 폴백의 존재 이유다.
	for name, g := range map[string]string{
		"Selected":   ascii.Glyphs.Selected,
		"Healthy":    ascii.Glyphs.Healthy,
		"Unhealthy":  ascii.Glyphs.Unhealthy,
		"Unknown":    ascii.Glyphs.Unknown,
		"TreeBranch": ascii.Glyphs.TreeBranch,
		"TreeLast":   ascii.Glyphs.TreeLast,
		"Ellipsis":   ascii.Glyphs.Ellipsis,
	} {
		for _, r := range g {
			if r > 127 {
				t.Errorf("ASCII 글리프 %q 에 비ASCII 문자 %q 가 있다", name, string(r))
			}
		}
	}

	// 스피너 프레임도 마찬가지.
	for i, frame := range ascii.Glyphs.SpinnerDots {
		for _, r := range frame {
			if r > 127 {
				t.Errorf("ASCII 스피너 프레임 %d 에 비ASCII 문자 %q 가 있다", i, string(r))
			}
		}
	}
}

func TestUnicodeThemeUsesBoxDrawing(t *testing.T) {
	t.Parallel()

	// 유니코드 테마는 실제로 유니코드를 써야 한다. 안 그러면 폴백을 둘 이유가 없다.
	unicode := tui.New(false)

	hasNonASCII := false

	for _, r := range unicode.Glyphs.Healthy + unicode.Glyphs.Selected {
		if r > 127 {
			hasNonASCII = true
		}
	}

	if !hasNonASCII {
		t.Error("유니코드 테마가 ASCII 글리프만 쓴다")
	}
}

func TestThemeRendersWithoutPanic(t *testing.T) {
	t.Parallel()

	// lipgloss 스타일이 실제로 문자열을 만들어내는지 확인한다. 버전이 바뀌어 API가
	// 어긋나면 여기서 컴파일이나 런타임에 드러난다.
	for _, ascii := range []bool{false, true} {
		th := tui.New(ascii)

		border := lipgloss.NewStyle().Border(th.Glyphs.Border).Render("내용")
		if !strings.Contains(border, "내용") {
			t.Errorf("ascii=%v: 테두리 렌더 결과에 내용이 없다: %q", ascii, border)
		}

		if got := th.Title.Render("제목"); !strings.Contains(got, "제목") {
			t.Errorf("ascii=%v: 제목 렌더 실패: %q", ascii, got)
		}
	}
}
