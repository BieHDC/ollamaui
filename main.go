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
	"biehdc.tool.ollamaui/widgetlist"

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
	var msgList *widgetlist.List
	msgList = widgetlist.NewList(
		// length
		func() int {
			return len(g.messages)
		},
		// create
		func() fyne.CanvasObject {
			ll := widget.NewLabel("example text")
			ll.Wrapping = fyne.TextWrapWord
			return ll
		},
		// update
		func(lii widgetlist.ListItemID, co fyne.CanvasObject) {
			item, ok := co.(*widget.Label)
			if !ok {
				panic("item is not label")
			}

			if g.messages[lii].Role == "user" {
				item.TextStyle = fyne.TextStyle{Bold: true}
			} else {
				item.TextStyle = fyne.TextStyle{}
			}

			// item.Importance = widget.MediumImportance // default // maybe deal with errors
			item.SetText(g.messages[lii].Content)
			item.Refresh()

			msgList.SetItemHeight(lii, float64(item.MinSize().Height)) // needed
		},
	)
	msgList.OnItemSecondaryTapped = func(id widgetlist.ListItemID, _ *fyne.PointEvent) {
		g.a.Clipboard().SetContent(g.messages[id].Content)
		if !fyne.CurrentDevice().IsMobile() {
			// android has an on screen notification when something has
			// been written to the clipboard, only show on desktop
			dialog.ShowInformation("Message Clipboarded", g.messages[id].Content, g.w)
		}
	}

	// chat history
	chathistory := g.a.Preferences().String("chathistory")
	if len(chathistory) > 0 {
		err := json.Unmarshal([]byte(chathistory), &g.messages)
		if err != nil {
			g.addStartfunc(func() { dialog.ShowError(fmt.Errorf("error loading chathistory: %w", err), g.w) })
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

	// scroll to bottom on start with this hack
	// and then nil ourselves to death
	var anim *fyne.Animation
	anim = fyne.NewAnimation(1*time.Second, func(f float32) {
		msgList.Refresh()
		msgList.ScrollToBottom()
		if int(f) == 100 {
			anim.Stop()
			anim = nil
		}
	})
	g.addStartfunc(anim.Start)

	// input handing
	clientCTX := context.Background()
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

		g.messages = append(g.messages, api.Message{
			Role:    "user",
			Content: s,
		})
		msgList.Refresh()
		msgList.ScrollToBottom()

		// grey out during response
		usermessage.Disable()
		usermessage.ActionItem.(fyne.Disableable).Disable()

		type opt struct {
			err error
			msg api.Message
		}
		msgflow := make(chan opt)
		go func() {
			defer close(msgflow)

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
				msg.Content = strings.TrimPrefix(msg.Content, " ")
				msgflow <- opt{msg: msg}
				return nil
			}

			err := client.Chat(clientCTX, req, respFunc)
			if err != nil {
				msgflow <- opt{err: err, msg: msg}
			}
		}()

		go func() {
			g.messages = append(g.messages, api.Message{})
			index := len(g.messages) - 1

			// when we error, we keep the prompt,
			// so the user can redispatch it
			preserveUsermessage := false
			// mainloop
			for msg := range msgflow {
				g.messages[index] = msg.msg
				fyne.DoAndWait(func() {
					if msg.err != nil {
						// yea i am doing this the lazy way
						msg.msg.Content += "\n\n" + "Error: " + msg.err.Error()
						g.messages[index] = msg.msg
						msgList.ScrollToBottom()
						preserveUsermessage = true
					}

					msgList.Refresh()
					if len(msg.msg.Content) < 800 {
						// fixme testing scroll along for short texts
						// but dont if its longer to not mess with reading
						// seems to work nicely, should be a per device setting
						// marking as todo until tested on phone
						msgList.ScrollToBottom()
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
		list, err := client.List(clientCTX)
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
		go warmCacheForModel(g.model, client)
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
					g.messages = []api.Message{}
				}
			}, setwin)
		})

		c := container.NewBorder(
			container.NewVBox(manualaddress, confirm),
			container.NewVBox(
				g.helpWidget(),
				deletechat,
				g.manualThemeScaler(),
				g.fyneSettings(),
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

	//bottom := container.NewVSplit(widgetlistcontainer, container.NewBorder(nil, nil, nil, nil, usermessage))
	bottom := container.NewVSplit(msgList, container.NewBorder(nil, nil, nil, nil, usermessage))
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
