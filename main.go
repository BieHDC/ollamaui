package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"biehdc.tool.ollamaui/theming"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/cmd/fyne_settings/settings"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ollama/ollama/api"
)

type gui struct {
	a fyne.App
	w fyne.Window
	//
	startfuncs []func()
	stopfuncs  []func()
	//
	model      string
	messages   []api.Message
	lastserver string // "" triggers first start behaviour
}

// fixme
// scroll down on keyboard open if already scrolled down
// mobile perf quickly degrages due to widgetism, try port widget list
// multiple tabs if popular demand/i myself need it
// search function?
// tiktok voicegen integration?
// >	https://github.com/gopxl/beep/tree/main
// >	https://github.com/ebitengine/oto
func main() {
	g := gui{}

	g.a = app.NewWithID("biehdc.priv.ollamagui")
	g.w = g.a.NewWindow("OllamaUI")

	// remember and restore window size
	width := g.a.Preferences().IntWithFallback("width", 400)
	height := g.a.Preferences().IntWithFallback("heigth", 600)
	g.w.Resize(fyne.NewSize(float32(width), float32(height)))
	g.addStopfunc(func() {
		sz := g.w.Canvas().Size()
		g.a.Preferences().SetInt("width", int(sz.Width))
		g.a.Preferences().SetInt("heigth", int(sz.Height))
	})

	// remember and restore the last used server
	client, _ := api.ClientFromEnvironment()
	g.lastserver = g.a.Preferences().String("lastserver")
	if g.lastserver != "" {
		base := url.URL{
			Scheme: "http",
			Host:   g.lastserver,
		}
		client = api.NewClient(&base, http.DefaultClient)
		err := client.Heartbeat(context.TODO())
		if err != nil {
			g.addStartfunc(func() { dialog.ShowError(errors.New("last used server could not be contacted"), g.w) })
		}
	}
	g.addStopfunc(func() { g.a.Preferences().SetString("lastserver", g.lastserver) })

	// message storage handling
	// fixme this needs a long convo perf check especially on mobile
	widgetlistcontainerinner := container.NewVBox()
	widgetlistcontainer := container.NewVScroll(widgetlistcontainerinner)
	appendMessage := func(s string, isuser bool) *widget.Label {
		ll := widget.NewLabel(s)
		ll.Wrapping = fyne.TextWrapWord
		if isuser == true {
			// this is user, fat text to distinguish
			ll.TextStyle = fyne.TextStyle{Bold: true}
		}
		widgetlistcontainerinner.Objects = append(widgetlistcontainerinner.Objects,
			NewSecondaryTapperLayer(ll, func(_ *fyne.PointEvent) {
				g.a.Clipboard().SetContent(ll.Text)
				if !fyne.CurrentDevice().IsMobile() {
					// android has an on screen notification when something has
					// been written to the clipboard, only show on desktop
					dialog.ShowInformation("Message Clipboarded", ll.Text, g.w)
				}
			}),
			widget.NewSeparator(),
		)
		widgetlistcontainer.Refresh()
		return ll
	}
	deletemessages := func() {
		// the gui
		widgetlistcontainerinner.Objects = []fyne.CanvasObject{}
		widgetlistcontainer.Refresh()
		// the ollama part
		g.messages = []api.Message{}
	}

	// chat history
	chathistory := g.a.Preferences().String("chathistory")
	if len(chathistory) > 0 {
		err := json.Unmarshal([]byte(chathistory), &g.messages)
		if err != nil {
			g.addStartfunc(func() { dialog.ShowError(fmt.Errorf("error loading chathistory: %w", err), g.w) })
		} else {
			// make all widgets
			for _, msg := range g.messages {
				appendMessage(msg.Content, msg.Role == "user")
			}
			widgetlistcontainer.ScrollToBottom()
		}
	}
	g.addStopfunc(func() {
		b, err := json.Marshal(g.messages)
		if err != nil {
			// we cant show a dialog on shutdown
			fmt.Printf("failed to save chathistory: %s\n", err)
			return
		}
		g.a.Preferences().SetString("chathistory", string(b))
	})

	// input handing
	ctx := context.Background()
	usermessage := NewEntryTroller()
	usermessage.ActionItem = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { usermessage.OnSubmitted(usermessage.Text) })
	usermessage.PlaceHolder = "Type your message..."
	usermessage.Text = g.a.Preferences().String("lastprompt")
	if usermessage.Text == "" {
		usermessage.Text = "Hello friend. What is your name and task?"
	}
	g.addStopfunc(func() { g.a.Preferences().SetString("lastprompt", usermessage.Text) })
	usermessage.SetMinRowsVisible(2)
	usermessage.Wrapping = fyne.TextWrapWord
	usermessage.MultiLine = true
	usermessage.OnSubmitted = func(s string) {
		if s == "" {
			return // ignore empty
		}

		appendMessage(s, true)
		widgetlistcontainer.ScrollToBottom()

		// grey out during response
		usermessage.Disable()
		usermessage.ActionItem.(fyne.Disableable).Disable()
		// this is kinda quick and dirty, but all options
		// to launch this multiple times are disabled
		// if unsure, handle this more explicit...

		type opt struct {
			err error
			msg string
		}
		msgflow := make(chan opt)
		go func() {
			defer close(msgflow)

			g.messages = append(g.messages, api.Message{
				Role:    "user",
				Content: s,
			})

			req := &api.ChatRequest{
				Model:    g.model,
				Messages: g.messages,
				// the following is there for user experience
				// techically the server should set the
				// "OLLAMA_KEEP_ALIVE=30min" environment variable
				KeepAlive: &api.Duration{Duration: 30 * time.Minute},
			}

			var msg api.Message
			msg.Role = "assistant"
			respFunc := func(resp api.ChatResponse) error {
				msg.Content += resp.Message.Content
				msgflow <- opt{msg: msg.Content}
				return nil
			}

			err := client.Chat(ctx, req, respFunc)
			if err != nil {
				msgflow <- opt{err: errors.New(strings.TrimSpace(msg.Content + "\n\nError: " + err.Error()))}
				return // bail to not add errored message to history and dont delete the prompt
			}

			g.messages = append(g.messages, msg)
		}()

		go func() {
			// setup
			var ll *widget.Label
			fyne.DoAndWait(func() {
				ll = appendMessage("", false)
			})

			// when we error, we keep the prompt,
			// so the user can redispatch it
			preserveUsermessage := false
			// mainloop
			for msg := range msgflow {
				fyne.DoAndWait(func() {
					if msg.err != nil {
						ll.Importance = widget.DangerImportance
						ll.SetText(msg.err.Error())
						widgetlistcontainer.ScrollToBottom()
						preserveUsermessage = true
					}

					ll.SetText(msg.msg)

					if len(msg.msg) < 800 {
						// fixme testing scroll along for short texts
						// but dont if its longer to not mess with reading
						// seems to work nicely, should be a per device setting
						// marking as todo until tested on phone
						widgetlistcontainer.ScrollToBottom()
					}
				})
			}

			// cleanup
			fyne.DoAndWait(func() {
				if !preserveUsermessage {
					usermessage.SetText("")
				}
				usermessage.ActionItem.(fyne.Disableable).Enable()
				usermessage.Enable()
			})
		}()
	}
	usermessage.Refresh()

	// model
	const nomodel = "NONE - refresh list"
	modelselection := widget.NewSelect([]string{}, func(s string) { g.model = s })
	modelselectionfunc := func() {
		list, err := client.List(ctx)
		if err != nil {
			if g.lastserver != "" {
				// dont pop the box on first starts
				dialog.ShowError(fmt.Errorf("cant retrieve model list: %w", err), g.w)
			}
			modelselection.PlaceHolder = nomodel
			modelselection.SetOptions([]string{})
			return
		}

		var available []string
		for _, m := range list.Models {
			available = append(available, m.Name)
		}
		modelselection.PlaceHolder = ""
		modelselection.SetOptions(available)
	}
	g.addStartfunc(modelselectionfunc, func() {
		g.model = g.a.Preferences().StringWithFallback("model", nomodel)
		modelselection.SetSelected(g.model)
		warmCacheForModel(g.model, client)
	})
	g.addStopfunc(func() { g.a.Preferences().SetString("model", g.model) })

	// settings window stuff
	settingswindow := func() {
		setwin := g.a.NewWindow("OllamaUI Settings")

		manualaddress := widget.NewEntry()
		manualaddress.PlaceHolder = "127.0.0.1:11434"
		manualaddress.SetText(g.lastserver)
		manualaddress.OnSubmitted = func(s string) {
			if s == "" {
				s = manualaddress.PlaceHolder
			}
			manualaddress.Text = s // for searchForHosts
			defer manualaddress.Refresh()

			base := url.URL{
				Scheme: "http",
				Host:   s,
			}
			version, err := testServer(&base)
			if err != nil {
				dialog.ShowError(fmt.Errorf("Failed: %w", err), setwin)
			} else {
				client = api.NewClient(&base, http.DefaultClient)
				g.lastserver = s
				dialog.ShowInformation("Success", "Ollama: "+version, setwin)
				modelselectionfunc() // get list and populate, make user select
			}
		}

		hosts := container.NewVBox(widget.NewLabel("Trying to search LAN for ollama..."))
		go searchForHosts(hosts, manualaddress.OnSubmitted)

		confirm := widget.NewButton("Confirm", func() { manualaddress.OnSubmitted(manualaddress.Text) })
		//
		deletechat := widget.NewButton("Delete History", func() {
			dialog.ShowConfirm("Delete Chat?", "Is this real?", func(b bool) {
				if b {
					deletemessages()
				}
			}, setwin)
		})

		c := container.NewBorder(
			container.NewVBox(manualaddress, confirm),
			container.NewVBox(
				g.helpwidget(),
				deletechat,
				g.manualthemescaler(),
				g.fynesettings(),
				widget.NewButton("Ok", func() { setwin.Close() }),
			),
			nil, nil,
			container.NewVScroll(hosts),
		)

		setwin.SetContent(c)
		setwin.Show()
		ss := setwin.Content().Size()
		setwin.Resize(ss.Add(ss))
		setwin.RequestFocus()
	}

	if g.lastserver == "" {
		// first start
		g.addStartfunc(func() {
			g.lastserver = "127.0.0.1:11434"
			settingswindow()
		})
	}

	// time to put it all together
	top := container.NewBorder(nil, nil, nil,
		container.NewHBox(
			widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), modelselectionfunc),
			widget.NewButtonWithIcon("", theme.SettingsIcon(), settingswindow),
		),
		modelselection,
	)

	bottom := container.NewVSplit(widgetlistcontainer, container.NewBorder(nil, nil, nil, nil, usermessage))
	bottom.Offset = 1.0 // top as big as possible
	content := container.NewBorder(top, nil, nil, nil, bottom)

	g.w.SetContent(content)
	g.setupLifecyclers()
	g.w.ShowAndRun()
}

// settings stuff
func searchForHosts(hosts *fyne.Container, selected func(string)) {
	setLabel := func(s string) {
		fyne.DoAndWait(func() {
			hosts.Objects[0].(*widget.Label).SetText(s)
			hosts.Refresh()
		})
	}
	appendObject := func(co fyne.CanvasObject) {
		fyne.DoAndWait(func() {
			hosts.Objects = append(hosts.Objects, co)
			hosts.Refresh()
		})
	}

	found := findInstances()
	num := 0
	for {
		h, ok := <-found
		if !ok { // we read all
			setLabel((fmt.Sprintf("Done Scanning, found %d", num)))
			break
		}
		if h.err != nil {
			appendObject(widget.NewLabel(h.err.Error()))
			continue
		}

		num++
		appendObject(widget.NewButtonWithIcon(fmt.Sprintf("%s (%s)", h.url.Host, h.version), theme.MoveUpIcon(), func() { selected(h.url.Host) }))
	}
}

func (g *gui) fynesettings() fyne.CanvasObject {
	return widget.NewButton("Appearance", func() {
		w := g.a.NewWindow("Fyne Settings")
		w.SetContent(settings.NewSettings().LoadAppearanceScreen(g.w))
		w.Resize(fyne.NewSize(440, 520))
		w.Show()
	})
}

func (g *gui) helpwidget() fyne.CanvasObject {
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

func (g *gui) manualthemescaler() fyne.CanvasObject {
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
