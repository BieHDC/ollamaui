package theming

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// My Theme with extra scaling features
type scaledTheme struct {
	fyne.Theme
	scale float32
}

func (st scaledTheme) Size(tsn fyne.ThemeSizeName) float32 {
	return st.Theme.Size(tsn) * st.scale
}

func newScaledTheme(scale float32) scaledTheme {
	return scaledTheme{
		Theme: theme.DefaultTheme(),
		scale: scale, //just a little nicer
	}
}

func ApplyTheme(app fyne.App, scale float32) {
	myTheme := newScaledTheme(scale)
	app.Settings().SetTheme(myTheme)
}

func PlatformDefaultScale[T float32 | float64]() T {
	return 1.0
	/*
		// This would need some fine tuning
		if fyne.CurrentDevice().IsMobile() {
			return 1.5
		} else {
			return 1.0
		}
	*/
}
