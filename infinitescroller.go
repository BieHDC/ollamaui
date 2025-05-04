package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

type lengthFuncInfiniteScroller func() int
type makeFuncInfiniteScroller func(lbound, ubound int) []fyne.CanvasObject

type infiniteScroller struct {
	// dynamic
	lbound  int                        // lower bound slice
	ubound  int                        // upper bound slice
	lenobjs lengthFuncInfiniteScroller // num elements
	// config
	maxobjs int // how many created objects at once
	// fyne
	scroll *container.Scroll
	// for GoToSpecific
	makefn makeFuncInfiniteScroller
	// scroll along when scrolled down
	atBottom bool
}

// other fixme
// appended objects when not at bottom will only appear
// after a scroll has happened. not relevant right now

func (is *infiniteScroller) GetCanvasObject() fyne.CanvasObject {
	return is.scroll
}

func (is *infiniteScroller) RefreshCurrent() {
	is.scroll.Content.(*fyne.Container).Objects = is.makefn(is.lbound, is.ubound)
	is.scroll.Content.Refresh()
}

func (is *infiniteScroller) GoToBottom() {
	is.ubound = is.lenobjs()
	is.lbound = is.ubound - is.maxobjs
	is.lbound = max(0, is.lbound)
	is.scroll.Content.(*fyne.Container).Objects = is.makefn(is.lbound, is.ubound)
	is.scroll.Content.Refresh()
	is.scroll.ScrollToBottom()
	is.atBottom = true // gets invalidated because reality changes
}

func (is *infiniteScroller) GoToBottomIfAtBottom() bool {
	if is.atBottom {
		is.GoToBottom()
	}
	return is.atBottom
}

func (is *infiniteScroller) GoToTop() {
	is.lbound = 0
	is.ubound = min(is.maxobjs, is.lenobjs())
	is.scroll.Content.(*fyne.Container).Objects = is.makefn(is.lbound, is.ubound)
	is.scroll.Content.Refresh()
	is.scroll.ScrollToTop()
	is.atBottom = false
}

func (is *infiniteScroller) GoToSpecific(index int) {
	// this is accurate enough. to be 100% we would need
	// to know the height of the indexoid and add that to
	// the scroll calculation. and that too brings its issues.
	is.lbound = index - (is.maxobjs / 2)
	is.lbound = max(0, is.lbound) // when going to something close to the top
	is.ubound = is.lbound + is.maxobjs
	is.ubound = min(is.ubound, is.lenobjs())         // when something is close to the bottom
	is.lbound = min(is.lbound, is.ubound-is.maxobjs) // prevent lbound>ubound

	// put the scrollbar into the center
	is.scroll.ScrollToOffset(fyne.NewPos(0, (is.scroll.Content.Size().Height/2)-(is.scroll.Size().Height/2)))

	// assume we want to stay at index and not be auto scrolled
	is.atBottom = false

	// fixme this code will work fine as long as you dont jump to anything close to the edges
	// and since this is made for batches of 1000s, i will keep the code like that for now
	// you are free to enhance it, or wait for me until i feel like it

	is.scroll.Content.(*fyne.Container).Objects = is.makefn(is.lbound, is.ubound)
	is.scroll.Refresh()
}

func newInfiniteScroller(lenfn lengthFuncInfiniteScroller, makefn makeFuncInfiniteScroller) *infiniteScroller {
	is := &infiniteScroller{}

	is.scroll = container.NewVScroll(nil)
	content := container.NewVBox()

	// the only 2 valid configurations so far: step 3 / maxobjs 12; step 5 / maxobjs 30
	step := 3
	is.maxobjs = 12
	is.lenobjs = lenfn
	is.lbound = max(0, is.lbound)
	is.ubound = min(is.maxobjs, is.lenobjs())
	// base
	content.Objects = makefn(is.lbound, is.ubound)
	// for GoToSpecific
	is.makefn = makefn

	// fixme when we are close to the bottom and something gets appended
	// to the slice, the next scroll after the append will jump because
	// the thing is directly adjusted.
	is.scroll.OnScrolled = func(p fyne.Position) {
		if (is.scroll.Content.Size().Height - is.scroll.Size().Height) == p.Y {
			is.atBottom = true
		} else {
			is.atBottom = false
		}

		lenobjs := is.lenobjs()
		changed := false
		var delta int
		rev := false

		//fmt.Println((p.Y / (is.scroll.Content.Size().Height-is.scroll.Size().Height))) 0.0 at top, 1.0 at bottom
		calced := p.Y / (is.scroll.Content.Size().Height - is.scroll.Size().Height)
		if calced > 0.80 {
			delta = lenobjs - (is.ubound + step)
			if delta < 0 {
				is.ubound = lenobjs
				is.lbound = is.ubound - is.maxobjs
				is.lbound = max(0, is.lbound)
			} else {
				is.lbound += step
				is.lbound = max(0, is.lbound)
				is.ubound += step
				is.ubound = min(is.ubound, lenobjs)
			}

			rev = true // walk the other way
			changed = true
			//
		} else if calced < 0.20 {
			delta = is.lbound - step
			if delta < 0 {
				is.lbound = 0
				is.ubound = min(is.maxobjs, lenobjs)
			} else {
				is.lbound -= step
				is.lbound = max(0, is.lbound)
				is.ubound -= step
				is.ubound = min(is.ubound, lenobjs)
			}
			changed = true
		}

		if changed {
			content.Objects = is.makefn(is.lbound, is.ubound)
			content.Refresh() //render deez
			if delta > 0 {

				// this is very fragile, dont change it.
				// see above for valid configs where this happens to best work out.
				// fixme certainly needs some clever code to be less jumpy, but is
				// about as stable as the average matrix client when scrolling.
				scrollOffset := (content.Size().Height - is.scroll.Size().Height) / float32(step)
				if rev {
					scrollOffset = -scrollOffset
				}

				// i hereby declare that this is defined behaviour and
				// will forever work like this
				is.scroll.ScrollToOffset(is.scroll.Offset.AddXY(0, scrollOffset))
			}
			is.scroll.Refresh()
		}
	}

	is.scroll.Content = content
	is.scroll.Refresh()

	return is
}
