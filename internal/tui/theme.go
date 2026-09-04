// Package tui는 cloudloupe의 대화형 터미널 UI다.
package tui

import (
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Glyphs는 테마가 사용하는 문자 집합이다.
//
// 유니코드를 못 그리는 터미널을 위한 ASCII 폴백을 렌더링 코드마다 if로 분기하지 않는다.
// 대신 테마가 문자를 들고 있고 호출부는 테마만 쓴다. 그래서 폴백 판단이 한곳에 모인다.
type Glyphs struct {
	Border      lipgloss.Border
	Selected    string
	Unselected  string
	Healthy     string
	Unhealthy   string
	Unknown     string
	TreeBranch  string
	TreeLast    string
	TreeVert    string
	Ellipsis    string
	SpinnerDots []string

	// Partial은 하위 항목 중 일부만 선택된 상태다. 서비스를 접어도 그 안에서 무엇을
	// 골랐는지 알 수 있어야 하므로, 전체 선택과 구분되는 문자가 필요하다.
	Partial string
	// Collapsed와 Expanded는 펼칠 수 있는 줄의 상태다.
	Collapsed string
	Expanded  string
}

// Theme는 UI 전체의 스타일과 문자를 담는다. 렌더링 코드는 이것만 받는다.
type Theme struct {
	Glyphs Glyphs
	ASCII  bool

	Title    lipgloss.Style
	Selected lipgloss.Style
	Normal   lipgloss.Style
	Faint    lipgloss.Style
	Warn     lipgloss.Style
	Error    lipgloss.Style
}

// unicodeGlyphs는 박스 문자와 기호를 쓰는 기본 문자 집합이다.
func unicodeGlyphs() Glyphs {
	return Glyphs{
		Border:      lipgloss.RoundedBorder(),
		Selected:    "▸",
		Unselected:  " ",
		Healthy:     "●",
		Unhealthy:   "✗",
		Unknown:     "○",
		TreeBranch:  "├─",
		TreeLast:    "└─",
		TreeVert:    "│ ",
		Ellipsis:    "…",
		SpinnerDots: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		Partial:     "◐",
		Collapsed:   "▸",
		Expanded:    "▾",
	}
}

// asciiGlyphs는 순수 ASCII 문자 집합이다.
//
// 유니코드 박스 문자가 깨지는 구형 Windows 콘솔(cmd.exe, 코드페이지 미변경)에서 쓴다.
func asciiGlyphs() Glyphs {
	return Glyphs{
		Border:      lipgloss.Border{Top: "-", Bottom: "-", Left: "|", Right: "|", TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+"},
		Selected:    ">",
		Unselected:  " ",
		Healthy:     "+",
		Unhealthy:   "x",
		Unknown:     "?",
		TreeBranch:  "|-",
		TreeLast:    "`-",
		TreeVert:    "| ",
		Ellipsis:    "...",
		SpinnerDots: []string{"|", "/", "-", "\\"},
		Partial:     "~",
		Collapsed:   ">",
		Expanded:    "v",
	}
}

// 색은 최소로 쓴다. 잘 만든 인프라 TUI(k9s, lazygit)의 관례를 따라 강조 1색과 경고·오류
// 2색만 두고, 본문은 터미널 기본색으로 남긴다. 색이 많으면 세로로 훑는 조회 화면에서 오히려
// 눈이 피로하다. 부차 정보는 색이 아니라 흐림(Faint)으로 눌러 대비를 만든다.
//
// AdaptiveColor를 쓰는 이유는 밝은 배경과 어두운 배경 터미널을 둘 다 자연스럽게 그리기
// 위함이다. lipgloss가 배경을 감지해 Light/Dark 중 하나를 고른다. 색을 못 쓰는 환경이나
// NO_COLOR에서는 lipgloss가 알아서 색을 떨어뜨리므로 여기서 따로 처리하지 않는다.
//
// 색은 변경 가능한 전역 변수가 아니라 함수로 둔다. 실행 중에 바뀌지 않고, 세 곳(테마 스타일과
// 테이블 선택 행)에서 같은 값을 참조하도록 한곳에 모은다.

// accentColor는 제목과 선택 강조에 쓰는 유일한 강조색이다. 청록 계열.
func accentColor() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: "6", Dark: "14"}
}

// warnColor는 주의가 필요한 값에 쓰는 노랑 계열이다.
func warnColor() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: "3", Dark: "11"}
}

// errorColor는 실패·오류에 쓰는 빨강 계열이다.
func errorColor() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: "1", Dark: "9"}
}

// New는 테마를 만든다. ascii가 true면 ASCII 폴백을 쓴다.
//
// ASCII 테마는 유니코드를 못 그리는 구형 콘솔용이다. 그런 환경은 색도 부실한 경우가 많아,
// 색 대신 굵기만으로 대비를 준다. 색 판단을 여기 한곳에 모아 렌더링 코드는 테마만 쓴다.
func New(ascii bool) Theme {
	glyphs := unicodeGlyphs()
	if ascii {
		glyphs = asciiGlyphs()
	}

	if ascii {
		return Theme{
			Glyphs:   glyphs,
			ASCII:    ascii,
			Title:    lipgloss.NewStyle().Bold(true),
			Selected: lipgloss.NewStyle().Bold(true),
			Normal:   lipgloss.NewStyle(),
			Faint:    lipgloss.NewStyle().Faint(true),
			Warn:     lipgloss.NewStyle().Bold(true),
			Error:    lipgloss.NewStyle().Bold(true),
		}
	}

	return Theme{
		Glyphs:   glyphs,
		ASCII:    ascii,
		Title:    lipgloss.NewStyle().Bold(true).Foreground(accentColor()),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(accentColor()),
		Normal:   lipgloss.NewStyle(),
		Faint:    lipgloss.NewStyle().Faint(true),
		Warn:     lipgloss.NewStyle().Bold(true).Foreground(warnColor()),
		Error:    lipgloss.NewStyle().Bold(true).Foreground(errorColor()),
	}
}

// DetectASCII는 현재 환경에서 ASCII 폴백을 써야 하는지 판단한다.
//
// 판단 순서:
//  1. CLOUDLOUPE_ASCII 가 설정되어 있으면 그 값을 따른다 (사용자가 직접 강제)
//  2. NO_COLOR 나 TERM=dumb 이면 최소 환경으로 보고 ASCII
//  3. Windows에서 UTF-8 코드페이지(65001)나 Windows Terminal이 아니면 ASCII
//  4. 그 외에는 유니코드
//
// force는 --ascii 플래그처럼 명시적으로 켠 경우다. 이때는 감지를 건너뛴다.
func DetectASCII(force bool) bool {
	if force {
		return true
	}

	// LookupEnv가 아니라 값으로 판단한다. 빈 값(CLOUDLOUPE_ASCII=)은 설정되지 않은 것으로
	// 봐야 한다. 빈 값을 "설정됨"으로 취급하면 감지 자체를 건너뛰어, 정작 폴백이 필요한
	// 구형 콘솔에서 유니코드로 빠진다.
	if v := strings.TrimSpace(os.Getenv("CLOUDLOUPE_ASCII")); v != "" {
		return isTruthy(v)
	}

	if os.Getenv("TERM") == "dumb" {
		return true
	}

	if runtime.GOOS == "windows" {
		return windowsNeedsASCII()
	}

	return false
}

// windowsNeedsASCII는 Windows 콘솔이 유니코드 박스 문자를 못 그릴 가능성이 높은지 본다.
//
// Windows Terminal(WT_SESSION 설정)과 UTF-8 코드페이지에서는 유니코드가 잘 나온다.
// 그 신호가 없는 전통적 cmd.exe / 구형 conhost 에서는 ASCII가 안전하다.
func windowsNeedsASCII() bool {
	if os.Getenv("WT_SESSION") != "" {
		return false
	}

	// ConEmu 계열 터미널도 유니코드를 지원한다.
	if os.Getenv("ConEmuANSI") == "ON" {
		return false
	}

	return true
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
