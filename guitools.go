package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/widget"
)

// Attach an arbitrary function to a secondary Tap
type secondaryTapper struct {
	widget.BaseWidget
	child fyne.CanvasObject
	cb    func(*fyne.PointEvent)
}

var _ fyne.SecondaryTappable = (*secondaryTapper)(nil)

func (st *secondaryTapper) TappedSecondary(pe *fyne.PointEvent) {
	if st.cb == nil {
		panic("no callback set, pointless use of widget")
	}
	st.cb(pe)
}

func NewSecondaryTapperLayer(child fyne.CanvasObject, cb func(*fyne.PointEvent)) *secondaryTapper {
	st := &secondaryTapper{child: child, cb: cb}
	st.ExtendBaseWidget(st)
	return st
}

func (st *secondaryTapper) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(st.child)
}

// Change the way widget.Entry works for this usecase
type entryTroller struct {
	widget.Entry
	selectKeyDown bool
}

func (e *entryTroller) Keyboard() mobile.KeyboardType {
	// pretend we are a single line keyboard with a submit key
	return mobile.SingleLineKeyboard
}

func (e *entryTroller) FocusLost() {
	e.selectKeyDown = false
	e.Entry.FocusLost()
}

func (e *entryTroller) KeyDown(key *fyne.KeyEvent) {
	if e.Disabled() {
		return
	}
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.selectKeyDown = true
	}
	e.Entry.KeyDown(key)
}

func (e *entryTroller) KeyUp(key *fyne.KeyEvent) {
	if e.Disabled() {
		return
	}
	if key.Name == desktop.KeyShiftLeft || key.Name == desktop.KeyShiftRight {
		e.selectKeyDown = false
	}
	e.Entry.KeyUp(key)
}

func (e *entryTroller) TypedKey(key *fyne.KeyEvent) {
	// even more tomfoolery because i dont feel like complying
	if key.Name == "Return" || key.Physical.ScanCode == 36 {
		// we have an enter key, do some trolling
		if e.selectKeyDown {
			// if we held the shift key, we do a newline
			e.Entry.TypedRune('\n')
		} else {
			// if we didnt hold the shift key, do a submit
			e.Entry.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft, Physical: fyne.HardwareKey{ScanCode: 50}})
			e.Entry.TypedKey(key)
			e.Entry.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft, Physical: fyne.HardwareKey{ScanCode: 50}})
		}
		return
	}
	e.Entry.TypedKey(key)
}

func NewEntryTroller() *entryTroller {
	e := &entryTroller{}
	e.ExtendBaseWidget(e)
	return e
}

// Lifecycle management helper
func (g *gui) addStartfunc(f ...func()) {
	g.startfuncs = append(g.startfuncs, f...)
}

func (g *gui) addStopfunc(f ...func()) {
	g.stopfuncs = append(g.stopfuncs, f...)
}

func (g *gui) setupLifecyclers() {
	g.a.Lifecycle().SetOnStarted(func() {
		for _, f := range g.startfuncs {
			f()
		}
	})
	g.a.Lifecycle().SetOnStopped(func() {
		for _, f := range g.stopfuncs {
			f()
		}
	})
}

/*
// Force a minimum size on a widget
type minSizer struct {
	widget.BaseWidget
	child   fyne.CanvasObject
	minsize fyne.Size
}

func NewMinSizer(child fyne.CanvasObject, ms fyne.Size) *minSizer {
	st := &minSizer{child: child, minsize: ms}
	st.ExtendBaseWidget(st)
	return st
}

func (st *minSizer) SetMinSize(size fyne.Size) {
	st.minsize = size
	st.Refresh()
}

func (st *minSizer) MinSize() fyne.Size {
	return st.child.MinSize().Max(st.minsize)
}

func (st *minSizer) Size() fyne.Size {
	return st.MinSize().Max(st.child.Size())
}

func (st *minSizer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(st.child)
}
*/
/*
	// needs some more thinking
	sendbutton := NewMinSizer(widget.NewButtonWithIcon("", theme.MailSendIcon(), func() {
		usermessage.OnSubmitted(usermessage.Text)
	}), fyne.NewSquareSize(1))

	readyfuncs = append(readyfuncs, func() {
		// looks better than the narrow button
		sz := sendbutton.Size()
		sendbutton.SetMinSize(fyne.NewSquareSize(max(sz.Height, sz.Width)))
	})
*/
