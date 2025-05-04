package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

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
	savefuncs  []func()
	//
	model      string
	messages   []api.Message
	lastserver string // "" triggers first start behaviour
	//
	msgscroller *infiniteScroller // for delete
}

// fixme
// go run -tags migrated_fynedo .
// multiple tabs if popular demand/i myself need it
// - if i do that i should also work to make it more resillient for various gui-races
// add scroll all up and down buttons to the infi widget
// - add a scroll down overlay button like a message client?
// - is that often enough used to warrent being added to the widget?
// - option to enable or disable said buttons showing?
// - needs widget with layouting and custom render etc, postponed
// search function?
// tiktok voicegen integration?
// >	https://github.com/gopxl/beep/tree/main
// >	https://github.com/ebitengine/oto

// i am calling this so often i want to cache it locally
var isMobile bool

func main() {
	g := gui{}

	g.a = app.NewWithID("biehdc.priv.ollamagui")
	g.w = g.a.NewWindow("OllamaUI")

	isMobile = fyne.CurrentDevice().IsMobile()

	// remember and restore window size
	width := g.a.Preferences().IntWithFallback("width", 400)
	height := g.a.Preferences().IntWithFallback("heigth", 600)
	g.w.Resize(fyne.NewSize(float32(width), float32(height)))
	g.addSavefunc(func() {
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
	g.addSavefunc(func() { g.a.Preferences().SetString("lastserver", g.lastserver) })

	// chat history
	chathistory := g.a.Preferences().String("chathistory")
	if len(chathistory) > 0 {
		err := json.Unmarshal([]byte(chathistory), &g.messages)
		if err != nil {
			g.addStartfunc(func() { dialog.ShowError(fmt.Errorf("error loading chathistory: %w", err), g.w) })
		}
	}
	g.addSavefunc(func() {
		b, err := json.Marshal(g.messages)
		if err != nil {
			// we cant show a dialog on shutdown
			fmt.Printf("failed to save chathistory: %s\n", err)
			return
		}
		g.a.Preferences().SetString("chathistory", string(b))
	})

	// display type
	lenfunc := func() int { return len(g.messages) }
	var makeMsgList makeFuncInfiniteScroller
	msgListContainer := container.NewStack()
	firstrun := true
	normalorrich := widget.NewRadioGroup([]string{"Normal", "Markdown"}, func(s string) {
		switch s {
		case "Normal":
			makeMsgList = g.makeNormal()
		case "Markdown":
			makeMsgList = g.makeMarkdown()
		}
		g.msgscroller = newInfiniteScroller(lenfunc, makeMsgList)
		// fixme this is a fyne bug about concurrent map read and write on richtext
		// fixed in next point release
		// replace firstrun/startfunc with fyne.Do() on update and see if that also
		// works for scrolling and being scrolled down on startup with a long history
		// so it does not jump to the middle
		if firstrun {
			g.addStartfunc(g.msgscroller.GoToBottom)
			firstrun = false
		} else {
			g.msgscroller.GoToBottom()
		}
		msgListContainer.Objects = []fyne.CanvasObject{g.msgscroller.GetCanvasObject()}
		msgListContainer.Refresh()
	})
	normalorrich.Horizontal = true
	normalorrich.SetSelected(g.a.Preferences().StringWithFallback("renderer", "Normal")) //invokes first create
	g.addSavefunc(func() { g.a.Preferences().SetString("renderer", normalorrich.Selected) })

	// input handing
	clientCTX := context.Background()
	usermessage := NewEntryTroller()
	if isMobile {
		// opening the softkeyboard resizes the window, but the scroll container
		// does not respect the fact that we are scrolled to the bottom. now when
		// the user wants to reply and opens the keyboard, he cant read the text
		// at the bottom for reference, and it looks bad too. the same behaviour
		// happens when you make a desktop window smaller, the scroll does not stay
		// scrolled to the very bottom if we are at the very bottom.
		// we can handle this case decently, we only cant handle a softkeyboard
		// closing itself and the user reopening it by clicking again before clicking
		// something else first. we manually call usermessage.FocusLost() in OnSubmitted
		// in the end so we can detect the next opening and do this dance again.
		usermessage.SetFocusGainedCallback(func() {
			go func() {
				// we need a delay, we cant query the animation
				// length, go with this. cope and seethe.
				<-time.After(1 * time.Second)
				fyne.Do(func() {
					g.msgscroller.GoToBottomIfAtBottom()
				})
			}()
		})
	}
	usermessage.ActionItem = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { usermessage.OnSubmitted(usermessage.Text) })
	usermessage.PlaceHolder = "Type your message..."
	usermessage.Text = g.a.Preferences().StringWithFallback("lastprompt", "Hello friend. What is your name and task?")
	g.addSavefunc(func() { g.a.Preferences().SetString("lastprompt", usermessage.Text) })
	usermessage.SetMinRowsVisible(2)
	usermessage.Wrapping = fyne.TextWrapWord
	usermessage.MultiLine = true
	usermessage.OnSubmitted = func(s string) {
		if s == "" {
			if isMobile {
				// tldr mobile softkeyboard close unfocus entry
				g.w.Canvas().Unfocus()
			}
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
		g.msgscroller.GoToBottom()

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
						dialog.ShowError(msg.err, g.w)
						preserveUsermessage = true
						// rollback - remove usermessage
						// and the space for the ai response
						g.messages = g.messages[:index-1]
					}

					if !g.msgscroller.GoToBottomIfAtBottom() {
						// we still need to refresh even
						// if we dont scroll to bottom
						g.msgscroller.RefreshCurrent()
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
				if isMobile {
					// for mobile softkeyboard and resizing and
					// scrolling reasons. see setFocusGainedCallback
					g.w.Canvas().Unfocus()
				}
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
	g.addSavefunc(func() { g.a.Preferences().SetString("model", g.model) })

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
							fyne.Do(func() {
								moveon = !usermessage.Disabled()
							})
							if moveon {
								break
							}
						}
						fyne.Do(func() {
							g.messages = []api.Message{}
							g.msgscroller.scroll.OnScrolled(fyne.Position{}) // redraw
						})
					}()
				}
			}, setwin)
		})

		okbutton := widget.NewButton("Ok", func() { setwin.Close() })
		okbutton.Importance = widget.HighImportance

		c := container.NewBorder(
			container.NewVBox(manualaddress, confirm),
			container.NewVBox(
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

	g.a.Lifecycle().SetOnExitedForeground(func() {
		for _, f := range g.savefuncs {
			f()
		}
	})
	g.a.Lifecycle().SetOnStarted(func() {
		for _, f := range g.startfuncs {
			f()
		}
	})
	if !isMobile {
		// this doesnt always work on mobile and
		// can cause corruption there. we will
		// only do SetOnExitedForeground here.
		g.a.Lifecycle().SetOnStopped(func() {
			for _, f := range g.savefuncs {
				f()
			}
		})
	}
	g.w.ShowAndRun()
}

func (g *gui) makeMarkdown() makeFuncInfiniteScroller {
	return func(lbound, ubound int) []fyne.CanvasObject {
		// you must ensure that lbound and ubound are valid
		objs := make([]fyne.CanvasObject, 0, (ubound-lbound)*2) // times 2 because we append 2 objects per iteration
		for i, msg := range g.messages[lbound:ubound] {
			themessage := msg
			themessage.Content = strings.TrimSpace(themessage.Content)

			item := widget.NewRichTextWithText("# heading")
			item.Wrapping = fyne.TextWrapWord

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
				}
			}

			item.Refresh()

			content := NewTapperLayer(item,
				// primary
				nil,
				// secondary
				func(_ *fyne.PointEvent) {
					g.a.Clipboard().SetContent(themessage.Content)
					if !isMobile {
						// android has an on screen notification when something has
						// been written to the clipboard, only show on desktop
						g.goodEnoughDialog("Message Clipboarded", themessage.Content)
					}
				},
				// double
				func(_ *fyne.PointEvent) {
					// we could alternatively change how item works, split it in 2 and do the hover thing from the dev version
					//dialog.ShowInformation("stuff", "do fun stuff", g.w)
					dialog.NewConfirm("Delete Message", "Delete this Message?", func(b bool) {
						if b {
							g.messages = slices.Delete(g.messages, lbound+i, lbound+i+1)
							g.msgscroller.scroll.OnScrolled(fyne.Position{}) // redraw

						}
					}, g.w).Show()
				})

			objs = append(objs, content, widget.NewSeparator())
		}

		return objs
	}

}

func (g *gui) makeNormal() makeFuncInfiniteScroller {
	return func(lbound, ubound int) []fyne.CanvasObject {
		// you must ensure that lbound and ubound are valid
		objs := make([]fyne.CanvasObject, 0, (ubound-lbound)*2) // times 2 because we append 2 objects per iteration
		for i, msg := range g.messages[lbound:ubound] {
			themessage := msg
			themessage.Content = strings.TrimSpace(themessage.Content)

			item := widget.NewLabel("*example text")
			item.Wrapping = fyne.TextWrapWord

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
					item.SetText(themessage.Content)
				}

				if lenmessage < 1 {
					item.SetText("Loading...")
				}
			}

			item.Refresh()

			content := NewTapperLayer(item,
				// primary
				func(_ *fyne.PointEvent) {
					message, found := strings.CutPrefix(themessage.Content, "<think>")
					if found && len(message) > 1 {
						thinkstring, _, found := strings.Cut(message, "</think>")
						if found {
							g.goodEnoughDialog("Thinker", thinkstring)
							return
						}
					}
					//dialog.ShowInformation("Thinker", "there is no thought", g.w)
				},
				// secondary
				func(_ *fyne.PointEvent) {
					g.a.Clipboard().SetContent(themessage.Content)
					if !isMobile {
						// android has an on screen notification when something has
						// been written to the clipboard, only show on desktop
						g.goodEnoughDialog("Message Clipboarded", themessage.Content)
					}
				},
				// double
				func(_ *fyne.PointEvent) {
					// we could alternatively change how item works, split it in 2 and do the hover thing from the dev version
					//dialog.ShowInformation("stuff", "do fun stuff", g.w)
					dialog.NewConfirm("Delete Message", "Delete this Message?", func(b bool) {
						if b {
							g.messages = slices.Delete(g.messages, lbound+i, lbound+i+1)
							g.msgscroller.scroll.OnScrolled(fyne.Position{}) // redraw

						}
					}, g.w).Show()
				},
			)

			objs = append(objs, content, widget.NewSeparator())
		}

		return objs
	}
}
