package main

import (
	"fmt"

	"biehdc.tool.ollamaui/theming"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/cmd/fyne_settings/settings"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
)

func (g *gui) fyneSettings() fyne.CanvasObject {
	return widget.NewButton("Appearance", func() {
		w := g.a.NewWindow("Fyne Settings")
		w.SetContent(settings.NewSettings().LoadAppearanceScreen(g.w))
		w.Resize(fyne.NewSize(440, 520))
		w.Show()
	})
}

func (g *gui) helpWidget() fyne.CanvasObject {
	return widget.NewButton("Information", func() {
		hwin := g.a.NewWindow("Information")
		hwin.SetContent(container.NewBorder(
			nil, widget.NewButton("Ok", func() { hwin.Close() }),
			nil, nil, helptext(),
		))
		hwin.Show()
		hwin.RequestFocus()
	})
}

func helptext() fyne.CanvasObject {
	s := "### Information\n"
	s += "- Right click to copy message\n"
	s += "- |>> long press on mobile\n"
	s += "- Do not force close the application\n"
	s += "- Make your ollama visible on LAN\n"
	s += "- |>> `OLLAMA_HOST=\"http://0.0.0.0:11434\"`\n"
	s += "- Make ollama keep the model longer in Cache\n"
	s += "- |>> `OLLAMA_KEEP_ALIVE=30m`\n"
	return widget.NewRichTextFromMarkdown(s)
}

func (g *gui) manualThemeScaler() fyne.CanvasObject {
	scalevalue := binding.NewString()
	scaleslider := widget.NewSlider(0.5, 3.0)
	scaleslider.Step = 0.1
	scaleslider.Value = g.a.Preferences().FloatWithFallback("appscale", theming.PlatformDefaultScale[float64]())
	scaleslider.OnChangeEnded = func(newvalue float64) {
		theming.ApplyTheme(g.a, float32(newvalue))
		scaleslider.Value = newvalue
		scaleslider.OnChanged(newvalue)
	}
	scaleslider.OnChanged = func(newvalue float64) {
		scalevalue.Set(fmt.Sprintf("%0.2f", newvalue))
	}
	scaleslider.OnChanged(scaleslider.Value)
	theming.ApplyTheme(g.a, float32(scaleslider.Value))
	g.addStopfunc(func() { g.a.Preferences().SetFloat("appscale", scaleslider.Value) })

	return container.NewBorder(
		nil, nil,
		widget.NewButton("Scaling", func() {
			//we do a little trolling
			scaleslider.OnChangeEnded(theming.PlatformDefaultScale[float64]())
		}),
		widget.NewLabelWithData(scalevalue),
		scaleslider,
	)
}
