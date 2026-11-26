package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"image"
	"image/draw"
	_ "image/png"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

//go:embed mygo.json
var colabOneManifestJSON []byte

const (
	colabOneSplashCardWidth    = 368
	colabOneSplashCardHeight   = 436
	colabOneSplashShadowMargin = 40
	colabOneSplashWindowWidth  = colabOneSplashCardWidth + colabOneSplashShadowMargin*2
	colabOneSplashWindowHeight = colabOneSplashCardHeight + colabOneSplashShadowMargin*2

	colabOneSplashMinDisplay         = 1200 * time.Millisecond
	colabOneSplashPaintGrace         = 180 * time.Millisecond
	colabOneSplashPageFailSafe       = 4500 * time.Millisecond
	colabOneSplashHardFailSafe       = 7500 * time.Millisecond
	colabOneSplashEnterDuration      = 280 * time.Millisecond
	colabOneSplashExitDuration       = 200 * time.Millisecond
	colabOneSplashDestroyDelay       = 230 * time.Millisecond
	colabOneSplashAvatarPedestalSize = float32(116)
	colabOneSplashAvatarTileSize     = float32(104)
	colabOneSplashSpinnerDuration    = 3000 * time.Millisecond
	colabOneSplashSpinnerArcSpan     = float32(0.75) // 3/4 circle (270° arc, 90° open gap)
	colabOneSplashSpinnerSize        = float32(72)
	colabOneSplashSpinnerRadius      = float32(28)
	colabOneSplashSpinnerStroke      = float32(6)
	colabOneAppearanceFileName       = "appearance.json"
	colabOneDefaultVersionString     = "v0.1.0"
)

var (
	colabOneSplashLogoOnce   sync.Once
	colabOneSplashLogoBitmap *ui.Bitmap
)

// colabOneAppVersion returns the formatted desktop version string from the
// embedded mygo.json manifest (e.g. "v0.1.0").
func colabOneAppVersion() string {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(colabOneManifestJSON, &manifest); err == nil {
		v := strings.TrimSpace(manifest.Version)
		if v != "" {
			if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
				return "v" + v[1:]
			}
			return "v" + v
		}
	}
	return colabOneDefaultVersionString
}

// colabOneTrimIconImage decodes a PNG icon and trims its outer transparent
// desktop-icon gutter so the white rounded tile fills splash hero slots cleanly.
func colabOneTrimIconImage(pngBytes []byte) (*image.NRGBA, error) {
	src, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	if b.Empty() {
		return nil, errors.New("empty icon image")
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), src, b.Min, draw.Src)

	minX, minY := b.Dx(), b.Dy()
	maxX, maxY := -1, -1
	for y := 0; y < b.Dy(); y++ {
		rowOffset := y * nrgba.Stride
		for x := 0; x < b.Dx(); x++ {
			if nrgba.Pix[rowOffset+x*4+3] > 8 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < minX || maxY < minY {
		return nrgba, nil
	}
	cropRect := image.Rect(minX, minY, maxX+1, maxY+1)
	cropped := image.NewNRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
	draw.Draw(cropped, cropped.Bounds(), nrgba, cropRect.Min, draw.Src)
	return cropped, nil
}

func colabOneSplashLogo() *ui.Bitmap {
	colabOneSplashLogoOnce.Do(func() {
		if img, err := colabOneTrimIconImage(trayIcon); err == nil {
			colabOneSplashLogoBitmap = ui.NewBitmap(img)
		} else if bmp, err2 := ui.DecodeBitmap(trayIcon); err2 == nil {
			colabOneSplashLogoBitmap = bmp
		}
	})
	return colabOneSplashLogoBitmap
}

// colabOneAppearanceStore persists the user's chosen appearance ("system",
// "light", or "dark") next to window-state.json so the native splash screen
// and main window frame open in the right theme before WebView initializes.
type colabOneAppearanceStore struct {
	mu     sync.Mutex
	pathFn func() (string, error)
}

func defaultColabOneAppearancePath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, colabOneAppearanceFileName), nil
}

func newColabOneAppearanceStore(pathFn func() (string, error)) *colabOneAppearanceStore {
	if pathFn == nil {
		pathFn = defaultColabOneAppearancePath
	}
	return &colabOneAppearanceStore{pathFn: pathFn}
}

func normalizeColabOneAppearance(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "system":
		return "system", true
	case "light":
		return "light", true
	case "dark":
		return "dark", true
	default:
		return "", false
	}
}

func (s *colabOneAppearanceStore) Load() string {
	if s == nil {
		return "system"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFn()
	if err != nil {
		return "system"
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return "system"
	}
	var payload struct {
		Appearance string `json:"appearance"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return "system"
	}
	if norm, ok := normalizeColabOneAppearance(payload.Appearance); ok {
		return norm
	}
	return "system"
}

func (s *colabOneAppearanceStore) Save(value string) error {
	if s == nil {
		return nil
	}
	norm, ok := normalizeColabOneAppearance(value)
	if !ok {
		return errors.New("unsupported appearance")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathFn()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(struct {
		Appearance string `json:"appearance"`
	}{Appearance: norm}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeColabOneFileAtomic(path, ".appearance-*.tmp", data, 0o644)
}

type colabOneSplashStrings struct {
	Brand    string
	Tagline  string
	Status   string
	Pillars  string
	Version  string
	A11yBusy string
}

func colabOneSplashCopy(locale string) colabOneSplashStrings {
	ver := colabOneAppVersion()
	switch locale {
	case colabOneLocaleEnUS:
		return colabOneSplashStrings{
			Brand:    "ColabOne",
			Tagline:  "Remote GPU Workspace",
			Status:   "Starting workspace",
			Pillars:  "GPU Instances · Public API · ComfyUI",
			Version:  ver,
			A11yBusy: "Starting ColabOne",
		}
	case colabOneLocaleZhTW:
		return colabOneSplashStrings{
			Brand:    "ColabOne",
			Tagline:  "遠端 GPU 工作區",
			Status:   "正在啟動工作區",
			Pillars:  "GPU 執行個體 · 對外 API · ComfyUI",
			Version:  ver,
			A11yBusy: "正在啟動 ColabOne",
		}
	case colabOneLocaleZhHK:
		return colabOneSplashStrings{
			Brand:    "ColabOne",
			Tagline:  "遠端 GPU 工作區",
			Status:   "正在啟動工作區",
			Pillars:  "GPU 實例 · 對外 API · ComfyUI",
			Version:  ver,
			A11yBusy: "正在啟動 ColabOne",
		}
	case colabOneLocaleJaJP:
		return colabOneSplashStrings{
			Brand:    "ColabOne",
			Tagline:  "リモート GPU ワークスペース",
			Status:   "ワークスペースを起動中",
			Pillars:  "GPU インスタンス · 外部 API · ComfyUI",
			Version:  ver,
			A11yBusy: "ColabOne を起動しています",
		}
	case colabOneLocaleKoKR:
		return colabOneSplashStrings{
			Brand:    "ColabOne",
			Tagline:  "원격 GPU 워크스페이스",
			Status:   "워크스페이스 시작 중",
			Pillars:  "GPU 인스턴스 · 외부 API · ComfyUI",
			Version:  ver,
			A11yBusy: "ColabOne을 시작하는 중",
		}
	default:
		return colabOneSplashStrings{
			Brand:    "ColabOne",
			Tagline:  "远端 GPU 工作空间",
			Status:   "正在启动工作空间",
			Pillars:  "GPU 实例 · 对外 API · ComfyUI",
			Version:  ver,
			A11yBusy: "正在启动 ColabOne",
		}
	}
}

type colabOneSplashPalette struct {
	cardTop          ui.Color
	cardBottom       ui.Color
	borderTop        ui.Color
	borderBottom     ui.Color
	shadowAmbient    ui.Color
	shadowKey        ui.Color
	auraPrimary      ui.Color
	auraSecondary    ui.Color
	ringStroke       ui.Color
	topSheen         ui.Color
	avatarHaloFill   ui.Color
	avatarHaloBorder ui.Color
	avatarHaloGlow   ui.Color
	logoShadow       ui.Color
	logoRing         ui.Color
	titleText        ui.Color
	taglineText      ui.Color
	statusText       ui.Color
	spinnerArc       ui.Color
	divider          ui.Color
	footerText       ui.Color
	versionBg        ui.Color
	versionBorder    ui.Color
	versionText      ui.Color
}

func resolveColabOneSplashPalette(dark bool) colabOneSplashPalette {
	if dark {
		return colabOneSplashPalette{
			cardTop:          ui.Hex("#181b24"),
			cardBottom:       ui.Hex("#101218"),
			borderTop:        ui.RGBA(255, 255, 255, 0.16),
			borderBottom:     ui.RGBA(255, 255, 255, 0.05),
			shadowAmbient:    ui.RGBA(0, 0, 0, 0.56),
			shadowKey:        ui.RGBA(0, 0, 0, 0.36),
			auraPrimary:      ui.RGBA(59, 130, 246, 0.22),
			auraSecondary:    ui.RGBA(99, 102, 241, 0.08),
			ringStroke:       ui.RGBA(255, 255, 255, 0.055),
			topSheen:         ui.RGBA(96, 165, 250, 0.48),
			avatarHaloFill:   ui.RGBA(255, 255, 255, 0.04),
			avatarHaloBorder: ui.RGBA(96, 165, 250, 0.26),
			avatarHaloGlow:   ui.RGBA(59, 130, 246, 0.24),
			logoShadow:       ui.RGBA(0, 0, 0, 0.48),
			logoRing:         ui.RGBA(255, 255, 255, 0.14),
			titleText:        ui.Hex("#f4f4f5"),
			taglineText:      ui.Hex("#9499a6"),
			statusText:       ui.Hex("#d4d4d8"),
			spinnerArc:       ui.Hex("#60a5fa"),
			divider:          ui.RGBA(255, 255, 255, 0.07),
			footerText:       ui.Hex("#787e8f"),
			versionBg:        ui.RGBA(255, 255, 255, 0.055),
			versionBorder:    ui.RGBA(255, 255, 255, 0.095),
			versionText:      ui.Hex("#9ca3b5"),
		}
	}
	return colabOneSplashPalette{
		cardTop:          ui.Hex("#ffffff"),
		cardBottom:       ui.Hex("#f7f9fc"),
		borderTop:        ui.RGBA(15, 23, 42, 0.09),
		borderBottom:     ui.RGBA(15, 23, 42, 0.12),
		shadowAmbient:    ui.RGBA(15, 23, 42, 0.14),
		shadowKey:        ui.RGBA(15, 23, 42, 0.07),
		auraPrimary:      ui.RGBA(59, 130, 246, 0.12),
		auraSecondary:    ui.RGBA(99, 102, 241, 0.045),
		ringStroke:       ui.RGBA(15, 23, 42, 0.045),
		topSheen:         ui.RGBA(59, 130, 246, 0.38),
		avatarHaloFill:   ui.RGBA(59, 130, 246, 0.04),
		avatarHaloBorder: ui.RGBA(59, 130, 246, 0.22),
		avatarHaloGlow:   ui.RGBA(59, 130, 246, 0.14),
		logoShadow:       ui.RGBA(15, 23, 42, 0.14),
		logoRing:         ui.RGBA(15, 23, 42, 0.08),
		titleText:        ui.Hex("#111827"),
		taglineText:      ui.Hex("#6b7280"),
		statusText:       ui.Hex("#4b5563"),
		spinnerArc:       ui.Hex("#3b82f6"),
		divider:          ui.RGBA(15, 23, 42, 0.065),
		footerText:       ui.Hex("#8891a2"),
		versionBg:        ui.RGBA(15, 23, 42, 0.045),
		versionBorder:    ui.RGBA(15, 23, 42, 0.08),
		versionText:      ui.Hex("#64748b"),
	}
}

type colabOneSplashViewState struct {
	mu              sync.Mutex
	shownAt         time.Time
	closing         bool
	closingAt       time.Time
	forceDark       *bool      // non-nil only in snapshot tests
	forceLocale     string     // non-empty only in snapshot tests
	staticFrame     bool       // true in deterministic snapshot renders
	staticSpinPhase float64    // optional fixed phase [0,1) when staticFrame is true
	forceNow        *time.Time // non-nil only in animation tests
}

func (s *colabOneSplashViewState) markClosing(now time.Time) {
	s.mu.Lock()
	if !s.closing {
		s.closing = true
		s.closingAt = now
	}
	s.mu.Unlock()
}

func (s *colabOneSplashViewState) render(c *ui.Context) {
	s.mu.Lock()
	shownAt := s.shownAt
	closing := s.closing
	closingAt := s.closingAt
	forceDark := s.forceDark
	forceLocale := s.forceLocale
	staticFrame := s.staticFrame
	staticSpinPhase := s.staticSpinPhase
	forceNow := s.forceNow
	s.mu.Unlock()

	dark := false
	if forceDark != nil {
		dark = *forceDark
	} else if t := c.Theme(); t != nil {
		dark = t.Dark
	}
	locale := colabOneGetLocale()
	if forceLocale != "" {
		locale = forceLocale
	}

	pal := resolveColabOneSplashPalette(dark)
	copyText := colabOneSplashCopy(locale)
	logo := colabOneSplashLogo()
	reduceMotion := c.Preferences().ReduceMotion

	// Transparent outer canvas gives the floating card generous shadow headroom.
	root := c.Root()
	root.Background(ui.Transparent).Fill().Center()

	var (
		cardOpacity float32 = 1
		cardScale   float32 = 1
		cardOffsetY float32 = 0
		auraPulse   float32 = 0.55
		animating   bool
	)

	if !staticFrame {
		now := time.Now()
		if forceNow != nil {
			now = *forceNow
		}
		if !shownAt.IsZero() {
			t := float32(now.Sub(shownAt).Seconds() / colabOneSplashEnterDuration.Seconds())
			if t < 0 {
				t = 0
			}
			if t < 1 {
				animating = true
				eased := 1 - (1-t)*(1-t)*(1-t)
				cardOpacity = eased
				if !reduceMotion {
					cardScale = 0.955 + 0.045*eased
					cardOffsetY = (1 - eased) * 8
				}
			}
		}
		if closing && !closingAt.IsZero() {
			t := float32(now.Sub(closingAt).Seconds() / colabOneSplashExitDuration.Seconds())
			if t < 0 {
				t = 0
			}
			if t < 1 {
				animating = true
			} else {
				t = 1
			}
			eased := t * t
			cardOpacity *= (1 - eased)
			if !reduceMotion {
				cardScale = 1 + 0.025*eased
			}
		}
		if animating {
			c.AnimationFrame()
		}
		if !reduceMotion {
			auraPulse = root.Loop("splash-aura", 3400*time.Millisecond, ui.Bounce(ui.EaseInOut))
		}
	}

	card := ui.Column(c).
		Size(colabOneSplashCardWidth, colabOneSplashCardHeight).
		Radius(24).
		LinearGradient(ui.LinearGradient{
			From:  pal.cardTop,
			To:    pal.cardBottom,
			Angle: 180,
		}).
		Border(1, ui.Color{}).
		BorderLinearGradient(ui.LinearGradient{
			From:  pal.borderTop,
			To:    pal.borderBottom,
			Angle: 180,
		}).
		Shadow(0, 20, 42, -4, pal.shadowAmbient).
		Shadow(0, 6, 16, -2, pal.shadowKey).
		Opacity(cardOpacity).
		Scale(cardScale, cardScale).
		Translate(0, cardOffsetY).
		DragWindow().
		Label(copyText.A11yBusy)

	card.Children(func() {
		// Clipped interior stage so ambient aura & orbital rings never bleed past rounded corners.
		ui.Column(c).Fill().Radius(24).Clip().Draw(func(p *ui.Painter, r ui.Rect) {
			if !staticFrame && animating {
				p.AnimationFrame()
			}
			// 1. Soft breathing Cobalt/Indigo studio aura centered behind the hero avatar.
			auraBoost := float32(0.86 + 0.28*auraPulse)
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.28},
				Radius: 210,
				Stops: []ui.GradientStop{
					{Color: pal.auraPrimary.Alpha(float32(pal.auraPrimary.A) / 255 * auraBoost), At: 0},
					{Color: pal.auraSecondary.Alpha(float32(pal.auraSecondary.A) / 255 * auraBoost), At: 0.48},
					{Color: ui.Transparent, At: 1},
				},
			}, 24)

			// 2. Crisp top-center specular accent sheen.
			sheenW := float32(136)
			sheenRect := ui.Rect{
				X: r.X + (r.W-sheenW)/2,
				Y: r.Y + 0.5,
				W: sheenW,
				H: 1.5,
			}
			p.FillGradient(sheenRect, ui.LinearGradient{
				Angle: 90,
				Stops: []ui.GradientStop{
					{Color: ui.Transparent, At: 0},
					{Color: pal.topSheen, At: 0.5},
					{Color: ui.Transparent, At: 1},
				},
			}, 1)

			// 3. Subtle concentric studio hairlines centered behind the hero avatar.
			cx := r.X + r.W/2
			cy := r.Y + 88
			radii := []float32{82, 120, 160}
			for i, rad := range radii {
				fade := float32(1.0 - float64(i)*0.26)
				var ring ui.Path
				ring.Circle(cx, cy, rad)
				p.StrokePath(&ring, 1, pal.ringStroke.Alpha(float32(pal.ringStroke.A)/255*fade))
			}
		}).Children(func() {
			// Main vertical brand & loading body:
			// Visual hierarchy: #1 Hero Avatar (largest) -> #2 3/4 Open-Gap Arc Spinner (second largest).
			ui.Column(c).
				Grow(1).
				FillWidth().
				Padding(30, 28, 22, 28).
				AlignItems(ui.Center).
				Children(func() {
					// #1 Largest focal element: ColabOne avatar pedestal.
					ui.Box(c).
						Size(colabOneSplashAvatarPedestalSize, colabOneSplashAvatarPedestalSize).
						Radius(28).
						Center().
						Background(pal.avatarHaloFill).
						Border(1.5, pal.avatarHaloBorder).
						Shadow(0, 8, 28, -6, pal.avatarHaloGlow).
						Children(func() {
							tile := ui.Box(c).
								Size(colabOneSplashAvatarTileSize, colabOneSplashAvatarTileSize).
								Radius(22).
								Center().
								Background(ui.Hex("#ffffff")).
								Border(1, pal.logoRing).
								Shadow(0, 8, 22, -2, pal.logoShadow)
							if logo != nil {
								tile.Children(func() {
									imgSize := colabOneSplashAvatarTileSize - 4
									ui.Image(c, logo).
										Size(imgSize, imgSize).
										Fit(ui.Contain).
										Radius(20).
										Clip()
								})
							}
						})

					// Brand wordmark directly beneath the hero avatar.
					ui.Text(c, copyText.Brand).
						Margin(16, 0, 0, 0).
						FontSize(24).
						FontWeight(700).
						LetterSpacing(-0.5).
						TextColor(pal.titleText).
						SingleLine()

					// Workspace subtitle beneath the brand name.
					ui.Text(c, copyText.Tagline).
						Margin(4, 0, 0, 0).
						FontSize(12.5).
						FontWeight(500).
						LetterSpacing(0.2).
						TextColor(pal.taglineText).
						SingleLine()

					ui.Spacer(c)

					// #2 Second-largest focal element: continuously rotating 3/4 open-gap circular arc
					// (270° arc, 90° open gap, 3 seconds per full revolution).
					ui.Box(c).
						Size(colabOneSplashSpinnerSize, colabOneSplashSpinnerSize).
						Draw(func(p *ui.Painter, r ui.Rect) {
							var phase float64
							if staticFrame {
								phase = staticSpinPhase
							} else {
								// Always request continuous frames for the loading arc so startup
								// clearly shows active rotation even when OS ReduceMotion is enabled.
								p.AnimationFrame()
								now := p.Now()
								if forceNow != nil {
									now = *forceNow
								}
								phase = colabOneSplashSpinPhase(now, shownAt, colabOneSplashSpinnerDuration)
							}
							cx := float64(r.X + r.W/2)
							cy := float64(r.Y + r.H/2)
							arc := buildColabOneArcPath(
								cx,
								cy,
								float64(colabOneSplashSpinnerRadius),
								phase*2*math.Pi,
								float64(colabOneSplashSpinnerArcSpan)*2*math.Pi,
							)
							p.StrokePath(&arc, colabOneSplashSpinnerStroke, pal.spinnerArc)
						})

					ui.Text(c, copyText.Status).
						Margin(12, 0, 0, 0).
						FontSize(13).
						FontWeight(500).
						LetterSpacing(0.2).
						TextColor(pal.statusText).
						SingleLine()

					ui.Spacer(c)
				})

			// Hairline divider separating main stage and studio footer signature.
			ui.Box(c).
				FillWidth().
				Padding(0, 24).
				Children(func() {
					ui.Box(c).
						FillWidth().
						Height(1).
						Background(pal.divider)
				})

			// Bottom studio signature row: workspace pillars + version badge.
			ui.Row(c).
				FillWidth().
				Height(44).
				Padding(0, 24).
				AlignItems(ui.Center).
				Children(func() {
					ui.Text(c, copyText.Pillars).
						FontSize(11).
						FontWeight(500).
						TextColor(pal.footerText).
						SingleLine()

					ui.Spacer(c)

					ui.Box(c).
						Padding(2, 8).
						Radius(999).
						Background(pal.versionBg).
						Border(1, pal.versionBorder).
						Children(func() {
							ui.Text(c, copyText.Version).
								FontSize(10.5).
								FontWeight(600).
								TextColor(pal.versionText).
								SingleLine()
						})
				})
		})
	})
}

// colabOneSplashSpinPhase returns the normalized clockwise rotation phase in [0, 1)
// for a continuous spinner with the given revolution period.
func colabOneSplashSpinPhase(now, origin time.Time, period time.Duration) float64 {
	if period <= 0 {
		return 0
	}
	var elapsed time.Duration
	if !origin.IsZero() {
		if now.Before(origin) {
			return 0
		}
		elapsed = now.Sub(origin)
	} else {
		elapsed = time.Duration(now.UnixNano())
	}
	rem := elapsed % period
	if rem < 0 {
		rem += period
	}
	return float64(rem) / float64(period)
}

// buildColabOneArcPath builds a single continuous open circular arc with round end
// caps from startAngle over sweepAngle radians (clockwise in screen coordinates),
// avoiding split-path seams and transform artifacts across GPU/software renderers.
func buildColabOneArcPath(cx, cy, rad, startAngle, sweepAngle float64) ui.Path {
	var path ui.Path
	path.Cap(ui.RoundCap)
	if rad <= 0 || sweepAngle <= 0 {
		return path
	}
	segments := int(math.Ceil(sweepAngle / (math.Pi / 2)))
	if segments < 1 {
		segments = 1
	}
	step := sweepAngle / float64(segments)
	k := (4.0 / 3.0) * math.Tan(step/4.0)

	x0 := cx + rad*math.Cos(startAngle)
	y0 := cy + rad*math.Sin(startAngle)
	path.MoveTo(float32(x0), float32(y0))

	for i := 0; i < segments; i++ {
		a0 := startAngle + float64(i)*step
		a1 := a0 + step
		cos0, sin0 := math.Cos(a0), math.Sin(a0)
		cos1, sin1 := math.Cos(a1), math.Sin(a1)

		sx, sy := cx+rad*cos0, cy+rad*sin0
		ex, ey := cx+rad*cos1, cy+rad*sin1
		c1x, c1y := sx-k*rad*sin0, sy+k*rad*cos0
		c2x, c2y := ex+k*rad*sin1, ey-k*rad*cos1
		path.CubeTo(
			float32(c1x), float32(c1y),
			float32(c2x), float32(c2y),
			float32(ex), float32(ey),
		)
	}
	return path
}

// colabOneStartupCoordinator orchestrates the cold-start handoff between the
// native splash window and the hidden main window so the main UI is revealed
// only once the page & initial snapshot are ready and the splash has completed
// a smooth minimum presentation beat.
type colabOneStartupCoordinator struct {
	mu               sync.Mutex
	minDisplay       time.Duration
	paintGrace       time.Duration
	pageFailSafe     time.Duration
	hardFailSafe     time.Duration
	afterFunc        func(time.Duration, func()) *time.Timer
	onReveal         func()
	pageReady        bool
	snapshotSettled  bool
	paintGraceDone   bool
	minDisplayDone   bool
	revealed         bool
	canceled         bool
	timers           []*time.Timer
}

type colabOneStartupConfig struct {
	MinDisplay   time.Duration
	PaintGrace   time.Duration
	PageFailSafe time.Duration
	HardFailSafe time.Duration
	AfterFunc    func(time.Duration, func()) *time.Timer
	OnReveal     func()
}

func newColabOneStartupCoordinator(cfg colabOneStartupConfig) *colabOneStartupCoordinator {
	if cfg.MinDisplay <= 0 {
		cfg.MinDisplay = colabOneSplashMinDisplay
	}
	if cfg.PaintGrace <= 0 {
		cfg.PaintGrace = colabOneSplashPaintGrace
	}
	if cfg.PageFailSafe <= 0 {
		cfg.PageFailSafe = colabOneSplashPageFailSafe
	}
	if cfg.HardFailSafe <= 0 {
		cfg.HardFailSafe = colabOneSplashHardFailSafe
	}
	if cfg.AfterFunc == nil {
		cfg.AfterFunc = time.AfterFunc
	}
	return &colabOneStartupCoordinator{
		minDisplay:   cfg.MinDisplay,
		paintGrace:   cfg.PaintGrace,
		pageFailSafe: cfg.PageFailSafe,
		hardFailSafe: cfg.HardFailSafe,
		afterFunc:    cfg.AfterFunc,
		onReveal:     cfg.OnReveal,
	}
}

func (c *colabOneStartupCoordinator) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.canceled || c.revealed {
		return
	}
	c.timers = append(c.timers,
		c.afterFunc(c.minDisplay, c.onMinDisplayElapsed),
		c.afterFunc(c.hardFailSafe, c.ForceReveal),
	)
}

func (c *colabOneStartupCoordinator) onMinDisplayElapsed() {
	c.mu.Lock()
	c.minDisplayDone = true
	shouldReveal := c.canRevealLocked()
	c.mu.Unlock()
	if shouldReveal {
		c.triggerReveal()
	}
}

func (c *colabOneStartupCoordinator) MarkPageReady() {
	c.mu.Lock()
	if c.canceled || c.revealed || c.pageReady {
		c.mu.Unlock()
		return
	}
	c.pageReady = true
	c.timers = append(c.timers, c.afterFunc(c.pageFailSafe, c.ForceReveal))
	shouldReveal := c.canRevealLocked()
	c.mu.Unlock()
	if shouldReveal {
		c.triggerReveal()
	}
}

func (c *colabOneStartupCoordinator) MarkSnapshotSettled() {
	c.mu.Lock()
	if c.canceled || c.revealed || c.snapshotSettled {
		c.mu.Unlock()
		return
	}
	c.snapshotSettled = true
	c.timers = append(c.timers, c.afterFunc(c.paintGrace, func() {
		c.mu.Lock()
		c.paintGraceDone = true
		shouldReveal := c.canRevealLocked()
		c.mu.Unlock()
		if shouldReveal {
			c.triggerReveal()
		}
	}))
	c.mu.Unlock()
}

func (c *colabOneStartupCoordinator) ForceReveal() {
	c.triggerReveal()
}

func (c *colabOneStartupCoordinator) IsActive() bool {
	c.mu.Lock()
	active := !c.revealed && !c.canceled
	c.mu.Unlock()
	return active
}

func (c *colabOneStartupCoordinator) Cancel() {
	c.mu.Lock()
	c.canceled = true
	timers := c.timers
	c.timers = nil
	c.mu.Unlock()
	for _, t := range timers {
		if t != nil {
			t.Stop()
		}
	}
}

func (c *colabOneStartupCoordinator) canRevealLocked() bool {
	return !c.canceled && !c.revealed && c.minDisplayDone && c.pageReady && c.paintGraceDone
}

func (c *colabOneStartupCoordinator) triggerReveal() {
	c.mu.Lock()
	if c.canceled || c.revealed {
		c.mu.Unlock()
		return
	}
	c.revealed = true
	timers := c.timers
	c.timers = nil
	cb := c.onReveal
	c.mu.Unlock()

	for _, t := range timers {
		if t != nil {
			t.Stop()
		}
	}
	if cb != nil {
		cb()
	}
}

// colabOneSplashPosition centers the floating splash window on the display
// where the main window will open. When the target is the primary display (or
// no saved state exists), it returns (0, 0) so MyGo's native DPI-aware
// centering places the window accurately.
func colabOneSplashPosition(winStore *colabOneWindowStateStore, width, height int) (int, int) {
	if winStore == nil {
		return 0, 0
	}
	states, err := winStore.LoadWindowStates()
	if err != nil {
		return 0, 0
	}
	st, ok := states["main"]
	if !ok || st.Width <= 0 || st.Height <= 0 {
		return 0, 0
	}
	primary := mygo.Screen.PrimaryDisplay().WorkArea
	target := mygo.Rectangle{X: st.X, Y: st.Y, Width: st.Width, Height: st.Height}
	workArea := mygo.Screen.DisplayMatching(target).WorkArea
	if workArea.Width <= 0 || workArea.Height <= 0 || workArea == primary {
		return 0, 0
	}
	return workArea.X + (workArea.Width-width)/2, workArea.Y + (workArea.Height-height)/2
}
