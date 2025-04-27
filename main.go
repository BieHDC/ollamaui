package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"biehdc.tool.ollamaui/widgetlist"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
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

	// display type
	var msgList *widgetlist.List
	msgListContainer := container.NewStack()
	normalorrich := widget.NewRadioGroup([]string{"Normal", "Markdown"}, func(s string) {
		switch s {
		case "Normal":
			msgList = g.makeNormal()
		case "Markdown":
			msgList = g.makeMarkdown()
		}
		msgListContainer.Objects = []fyne.CanvasObject{msgList}
		msgListContainer.Refresh()
	})
	normalorrich.Horizontal = true
	normalorrich.SetSelected(g.a.Preferences().StringWithFallback("renderer", "Normal"))
	g.addStopfunc(func() { g.a.Preferences().SetString("renderer", normalorrich.Selected) })

	// scroll limit
	const defaultScroller = 800
	scroller := g.a.Preferences().IntWithFallback("scrolllimit", defaultScroller)
	scrollLimit := widget.NewEntry()
	scrollLimit.ActionItem = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { scrollLimit.OnSubmitted(scrollLimit.Text) })
	scrollLimit.PlaceHolder = fmt.Sprintf("%d", defaultScroller)
	scrollLimit.Text = fmt.Sprintf("%d", scroller)
	g.addStopfunc(func() { g.a.Preferences().SetInt("scrolllimit", scroller) })
	//scrollLimit.OnSubmitted is being set by settingswindow
	scrollLimitSubmitted := func(s string) error {
		if s == "" {
			// apply the default in this case
			s = scrollLimit.PlaceHolder
			scrollLimit.Text = s
			scrollLimit.Refresh()
		}

		asnumber, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("%s is not a number", s)
		}
		scroller = asnumber
		return nil
	}
	scrollLimit.Refresh()
	scrollLimitSubmitted(scrollLimit.Text)

	// input handing
	clientCTX := context.Background()
	usermessage := NewEntryTroller()
	usermessage.ActionItem = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { usermessage.OnSubmitted(usermessage.Text) })
	usermessage.PlaceHolder = "Type your message..."
	usermessage.Text = g.a.Preferences().StringWithFallback("lastprompt", "Hello friend. What is your name and task?")
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

		// grey out during response
		usermessage.Disable()
		usermessage.ActionItem.(fyne.Disableable).Disable()

		req := &api.ChatRequest{
			Model:    g.model,
			Messages: g.messages,
			// the following is there for user experience
			// techically the server should set the
			// "OLLAMA_KEEP_ALIVE=30min" environment variable
			KeepAlive: &api.Duration{Duration: 30 * time.Minute},
		}

		g.messages = append(g.messages, api.Message{})
		index := len(g.messages) - 1
		msgList.Refresh()
		msgList.ScrollToBottom()

		type opt struct {
			err error
			msg api.Message
		}
		msgflow := make(chan opt)
		go func() {
			defer close(msgflow)

			var msg api.Message
			msg.Role = "assistant"
			respFunc := func(resp api.ChatResponse) error {
				msg.Content += resp.Message.Content
				msgflow <- opt{msg: msg}
				return nil
			}

			err := client.Chat(clientCTX, req, respFunc)
			if err != nil {
				msgflow <- opt{err: err, msg: msg}
			}
		}()

		go func() {
			// when we error, we keep the prompt,
			// so the user can redispatch it
			preserveUsermessage := false
			// mainloop
			for msg := range msgflow {
				fyne.DoAndWait(func() {
					g.messages[index] = msg.msg

					if msg.err != nil {
						// yea i am doing this the lazy way
						msg.msg.Content += "\n\n" + "Error: " + msg.err.Error()
						g.messages[index] = msg.msg
						msgList.ScrollToBottom()
						preserveUsermessage = true
					}

					msgList.Refresh()
					if len(msg.msg.Content) < scroller {
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
	settingswindowlock := false
	settingswindow := func() {
		if settingswindowlock {
			return
		}
		settingswindowlock = true

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
					go func() {
						// another lazy fix to a simple problem
						// dont be able to delete chat while outputting
						moveon := false
						for {
							fyne.DoAndWait(func() {
								moveon = !usermessage.Disabled()
							})
							if moveon {
								break
							}
						}
						fyne.Do(func() {
							g.messages = []api.Message{}
							msgListContainer.Refresh()
						})
					}()
				}
			}, setwin)
		})

		okbutton := widget.NewButton("Ok", func() { setwin.Close() })
		okbutton.Importance = widget.HighImportance

		// we need a little trixxing
		scrollLimit.OnSubmitted = func(s string) {
			err := scrollLimitSubmitted(s)
			if err != nil {
				dialog.ShowError(err, setwin)
			}
		}

		c := container.NewBorder(
			container.NewVBox(manualaddress, confirm),
			container.NewVBox(
				container.NewBorder(nil, nil, widget.NewLabel("Scroll-Along Limit:"), nil, scrollLimit),
				container.NewHBox(widget.NewLabel("Render:"), container.NewCenter(normalorrich)),
				g.helpWidget(),
				deletechat,
				g.manualThemeScaler(),
				g.fyneSettings(),
				okbutton,
			),
			nil, nil,
			container.NewVScroll(hosts),
		)

		normalorrich.Refresh() // otherwise doesnt always properly render

		setwin.SetOnClosed(func() { settingswindowlock = false })

		setwin.SetContent(c)
		setwin.Show()
		// better min size
		ss := setwin.Content().Size()
		ss = ss.AddWidthHeight(ss.Width/2, ss.Height/2)
		setwin.Resize(ss)
		setwin.RequestFocus()
		go setwin.CenterOnScreen() // hangs otherwise
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

	bottom := container.NewVSplit(msgListContainer, container.NewBorder(nil, nil, nil, nil, usermessage))
	bottom.Offset = 1.0 // top as big as possible
	content := container.NewBorder(top, nil, nil, nil, bottom)

	g.w.SetContent(content)
	g.setupLifecyclers()
	g.w.ShowAndRun()
}

func (g *gui) makeMarkdown() *widgetlist.List {
	var msgList *widgetlist.List
	msgList = widgetlist.NewList(
		// length
		func() int {
			return len(g.messages)
		},
		// create
		func() fyne.CanvasObject {
			rt := widget.NewRichTextWithText("# heading")
			rt.Wrapping = fyne.TextWrapWord
			return rt
		},
		// update
		func(lii widgetlist.ListItemID, co fyne.CanvasObject) {
			item, ok := co.(*widget.RichText)
			if !ok {
				panic("item is not a rich text")
			}

			themessage := g.messages[lii]

			if themessage.Role == "user" {
				item.ParseMarkdown("### User  \n")
				item.AppendMarkdown(themessage.Content)
			} else {
				message, found := strings.CutPrefix(themessage.Content, "<think>")
				lenmessage := len(message)

				item.ParseMarkdown("### Assistant  \n")

				if found && lenmessage > 1 {
					thinkstring, outputstring, found := strings.Cut(message, "</think>")

					if !found {
						// if we are still thinking, show the thinking
						// i wanted to just make this italic because it
						// looks cool, but no matter what, i am not allowed
						// so you get this
						item.AppendMarkdown("* Currently Thinking...")
						item.AppendMarkdown(thinkstring)
					} else {
						// if we are done thinking we render the output and hide the think
						link := &widget.HyperlinkSegment{
							Text: "Show Think",
							OnTapped: func() {
								g.goodEnoughDialog("Think Text", thinkstring)
							},
						}

						item.Segments = append(item.Segments, link, &widget.TextSegment{ /*make a newline*/ })
						item.AppendMarkdown(outputstring)
					}
				} else if !found {
					// otherwise its simple
					item.AppendMarkdown(themessage.Content)
				}

				if lenmessage < 1 {
					item.AppendMarkdown("Loading...")
					msgList.ScrollToBottom()
				}
			}

			item.Refresh()
			msgList.SetItemHeight(lii, float64(item.MinSize().Height)) // needed
		},
	)

	g.finaliseList(msgList)

	return msgList
}

func (g *gui) makeNormal() *widgetlist.List {
	var msgList *widgetlist.List
	msgList = widgetlist.NewList(
		// length
		func() int {
			return len(g.messages)
		},
		// create
		func() fyne.CanvasObject {
			ll := widget.NewLabel("*example text")
			ll.Wrapping = fyne.TextWrapWord
			return ll

		},
		// update
		func(lii widgetlist.ListItemID, co fyne.CanvasObject) {
			item, ok := co.(*widget.Label)
			if !ok {
				panic("item is not label")
			}

			themessage := g.messages[lii]

			if themessage.Role == "user" {
				item.TextStyle = fyne.TextStyle{Bold: true}
				item.SetText(themessage.Content)
			} else {
				message, found := strings.CutPrefix(themessage.Content, "<think>")
				lenmessage := len(message)

				item.TextStyle = fyne.TextStyle{}

				if found && lenmessage > 1 {
					thinkstring, outputstring, found := strings.Cut(message, "</think>")

					if !found {
						item.TextStyle = fyne.TextStyle{Italic: true}
						item.SetText(strings.TrimSpace(thinkstring))
					} else {
						item.SetText(strings.TrimSpace(outputstring))
					}
				} else if !found {
					// otherwise its simple
					item.SetText(strings.TrimSpace(themessage.Content))
				}

				if lenmessage < 1 {
					item.SetText("Loading...")
					msgList.ScrollToBottom()
				}
			}

			item.Refresh()
			msgList.SetItemHeight(lii, float64(item.MinSize().Height)) // needed
		},
	)

	g.finaliseList(msgList)
	msgList.OnItemTapped = func(id widgetlist.ListItemID, _ *fyne.PointEvent) {
		msg := g.messages[id].Content

		message, found := strings.CutPrefix(msg, "<think>")
		if found && len(message) > 1 {
			thinkstring, _, found := strings.Cut(message, "</think>")
			if found {
				g.goodEnoughDialog("Thinker", strings.TrimSpace(thinkstring))
				return
			}
		}
		//dialog.ShowInformation("Thinker", "there is no thought", g.w)
	}

	return msgList
}

func (g *gui) finaliseList(msgList *widgetlist.List) {
	msgList.OnItemSecondaryTapped = func(id widgetlist.ListItemID, _ *fyne.PointEvent) {
		g.a.Clipboard().SetContent(g.messages[id].Content)
		if !fyne.CurrentDevice().IsMobile() {
			// android has an on screen notification when something has
			// been written to the clipboard, only show on desktop
			g.goodEnoughDialog("Message Clipboarded", g.messages[id].Content)
		}
	}

	// scroll to bottom on start with this hack
	// and then nil ourselves to death
	var anim *fyne.Animation
	anim = fyne.NewAnimation(4*time.Second, func(f float32) {
		msgList.Refresh()
		msgList.ScrollToBottom()
		if int(f) == 100 {
			anim.Stop()
			anim = nil
		}
	})
	// yes we do need a little dance here
	g.addStartfunc(func() {
		go fyne.Do(anim.Start)
	})
}
