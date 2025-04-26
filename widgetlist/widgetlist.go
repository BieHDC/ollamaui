package widgetlist

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ListItemID uniquely identifies an item within a list.
type ListItemID = int

// Declare conformity with interfaces.
var _ fyne.Widget = (*List)(nil)

// List is a widget that pools list items for performance and
// lays the items out in a vertical direction inside of a scroller.
// By default, List requires that all items are the same size, but specific
// rows can have their heights set with SetItemHeight.
//
// Since: 1.4
type List struct {
	widget.BaseWidget

	// Length is a callback for returning the number of items in the list.
	Length func() int `json:"-"`

	// CreateItem is a callback invoked to create a new widget to render
	// a row in the list.
	CreateItem func() fyne.CanvasObject `json:"-"`

	// UpdateItem is a callback invoked to update a list row widget
	// to display a new row in the list. The UpdateItem callback should
	// only update the given item, it should not invoke APIs that would
	// change other properties of the list itself.
	UpdateItem func(id ListItemID, item fyne.CanvasObject) `json:"-"`

	// Item Tapped
	OnItemTapped func(id ListItemID, pe *fyne.PointEvent) `json:"-"`

	// Item Secondary Tapped
	OnItemSecondaryTapped func(id ListItemID, pe *fyne.PointEvent) `json:"-"`

	// HideSeparators hides the separators between list rows
	//
	// Since: 2.5
	HideSeparators bool

	scroller      *container.Scroll
	itemMin       fyne.Size
	itemHeights   map[ListItemID]float64
	offsetY       float64
	offsetUpdated func(fyne.Position)
}

// NewList creates and returns a list widget for displaying items in
// a vertical layout with scrolling and caching for performance.
//
// Since: 1.4
func NewList(length func() int, createItem func() fyne.CanvasObject, updateItem func(ListItemID, fyne.CanvasObject)) *List {
	list := &List{Length: length, CreateItem: createItem, UpdateItem: updateItem}
	list.ExtendBaseWidget(list)
	return list
}

// NewListWithData creates a new list widget that will display the contents of the provided data.
//
// Since: 2.0
func NewListWithData(data binding.DataList, createItem func() fyne.CanvasObject, updateItem func(binding.DataItem, fyne.CanvasObject)) *List {
	l := NewList(
		data.Length,
		createItem,
		func(i ListItemID, o fyne.CanvasObject) {
			item, err := data.GetItem(i)
			if err != nil {
				fyne.LogError(fmt.Sprintf("Error getting data item %d", i), err)
				return
			}
			updateItem(item, o)
		})

	data.AddListener(binding.NewDataListener(l.Refresh))
	return l
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (l *List) CreateRenderer() fyne.WidgetRenderer {
	l.ExtendBaseWidget(l)

	if f := l.CreateItem; f != nil && l.itemMin.IsZero() {
		item := createItemAndApplyThemeScope(f)

		l.itemMin = item.MinSize()
	}

	layout := &fyne.Container{Layout: newListLayout(l)}
	l.scroller = container.NewVScroll(layout)
	layout.Resize(layout.MinSize())
	objects := []fyne.CanvasObject{l.scroller}
	return newListRenderer(objects, l, l.scroller, layout)
}

// MinSize returns the size that this widget should not shrink below.
func (l *List) MinSize() fyne.Size {
	l.ExtendBaseWidget(l)
	return l.BaseWidget.MinSize()
}

// RefreshItem refreshes a single item, specified by the item ID passed in.
//
// Since: 2.4
func (l *List) RefreshItem(id ListItemID) {
	if l.scroller == nil {
		return
	}
	l.BaseWidget.Refresh()
	lo := l.scroller.Content.(*fyne.Container).Layout.(*listLayout)
	item, ok := lo.searchVisible(lo.visible, id)
	if ok {
		lo.setupListItem(item, id)
	}
}

// SetItemHeight supports changing the height of the specified list item. Items normally take the height of the template
// returned from the CreateItem callback. The height parameter uses the same units as a fyne.Size type and refers
// to the internal content height not including the divider size.
//
// Since: 2.3
func (l *List) SetItemHeight(id ListItemID, height float64) {
	if l.itemHeights == nil {
		l.itemHeights = make(map[ListItemID]float64)
	}

	refresh := l.itemHeights[id] != height
	l.itemHeights[id] = height

	if refresh {
		l.RefreshItem(id)
	}
}

func (l *List) scrollTo(id ListItemID) {
	if l.scroller == nil {
		return
	}

	separatorThickness := float64(l.Theme().Size(theme.SizeNamePadding))
	y := float64(0)
	lastItemHeight := float64(l.itemMin.Height)

	if len(l.itemHeights) == 0 {
		y = (float64(id) * float64(l.itemMin.Height)) + (float64(id) * separatorThickness)
	} else {
		i := 0
		for ; i < id; i++ {
			height := float64(l.itemMin.Height)
			if h, ok := l.itemHeights[i]; ok {
				height = h
			}

			y += height + separatorThickness
		}
		lastItemHeight = float64(l.itemMin.Height)
		if h, ok := l.itemHeights[i]; ok {
			lastItemHeight = h
		}
	}
	if y < float64(l.scroller.Offset.Y) {
		l.scroller.Offset.Y = float32(y)
	} else if y+float64(l.itemMin.Height) > float64(l.scroller.Offset.Y)+float64(l.scroller.Size().Height) {
		l.scroller.Offset.Y = float32(y + lastItemHeight - float64(l.scroller.Size().Height))
	}
	l.offsetUpdated(l.scroller.Offset)
}

// Resize is called when this list should change size. We refresh to ensure invisible items are drawn.
func (l *List) Resize(s fyne.Size) {
	l.BaseWidget.Resize(s)
	if l.scroller == nil {
		return
	}

	l.offsetUpdated(l.scroller.Offset)
	l.scroller.Content.(*fyne.Container).Layout.(*listLayout).updateList(true)
}

// ScrollTo scrolls to the item represented by id
//
// Since: 2.1
func (l *List) ScrollTo(id ListItemID) {
	length := 0
	if f := l.Length; f != nil {
		length = f()
	}
	if id < 0 || id >= length {
		return
	}
	l.scrollTo(id)
	l.Refresh()
}

// ScrollToBottom scrolls to the end of the list
//
// Since: 2.1
func (l *List) ScrollToBottom() {
	l.scroller.ScrollToBottom()
	l.offsetUpdated(l.scroller.Offset)
}

// ScrollToTop scrolls to the start of the list
//
// Since: 2.1
func (l *List) ScrollToTop() {
	l.scroller.ScrollToTop()
	l.offsetUpdated(l.scroller.Offset)
}

// ScrollToOffset scrolls the list to the given offset position.
//
// Since: 2.5
func (l *List) ScrollToOffset(offset float64) {
	if l.scroller == nil {
		return
	}
	if offset < 0 {
		offset = 0
	}
	contentHeight := float64(l.contentMinSize().Height)
	if float64(l.Size().Height) >= contentHeight {
		return // content fully visible - no need to scroll
	}
	if offset > contentHeight {
		offset = contentHeight
	}
	l.scroller.ScrollToOffset(fyne.NewPos(0, float32(offset)))
	l.offsetUpdated(l.scroller.Offset)
}

// GetScrollOffset returns the current scroll offset position
//
// Since: 2.5
func (l *List) GetScrollOffset() float64 {
	return l.offsetY
}

func (l *List) contentMinSize() fyne.Size {
	separatorThickness := float64(l.Theme().Size(theme.SizeNamePadding))
	if l.Length == nil {
		return fyne.NewSize(0, 0)
	}
	items := l.Length()

	if len(l.itemHeights) == 0 {
		return fyne.NewSize(l.itemMin.Width,
			float32((float64(l.itemMin.Height)+separatorThickness)*float64(items)-separatorThickness))
	}

	height := float64(0)
	totalCustom := 0
	templateHeight := float64(l.itemMin.Height)
	for id, itemHeight := range l.itemHeights {
		if id < items {
			totalCustom++
			height += itemHeight
		}
	}
	height += float64(items-totalCustom) * templateHeight

	return fyne.NewSize(l.itemMin.Width, float32(height+separatorThickness*float64(items-1)))
}

// fills l.visibleRowHeights and also returns offY and minRow
func (l *listLayout) calculateVisibleRowHeights(itemHeight float64, length int, th fyne.Theme) (offY float64, minRow int) {
	rowOffset := float64(0)
	isVisible := false
	l.visibleRowHeights = l.visibleRowHeights[:0]

	if l.list.scroller.Size().Height <= 0 {
		return
	}

	padding := float64(th.Size(theme.SizeNamePadding))

	if len(l.list.itemHeights) == 0 {
		paddedItemHeight := itemHeight + padding

		offY = float64(math.Floor(float64(l.list.offsetY/paddedItemHeight))) * paddedItemHeight
		minRow = int(math.Floor(float64(offY / paddedItemHeight)))
		maxRow := int(math.Ceil(float64((offY + float64(l.list.scroller.Size().Height)) / paddedItemHeight)))

		if minRow > length-1 {
			minRow = length - 1
		}
		if minRow < 0 {
			minRow = 0
			offY = 0
		}

		if maxRow > length-1 {
			maxRow = length - 1
		}

		for i := 0; i <= maxRow-minRow; i++ {
			l.visibleRowHeights = append(l.visibleRowHeights, itemHeight)
		}
		return
	}

	//for i := 0; i < length; i++ {
	// modernised
	for i := range length {
		height := itemHeight
		if h, ok := l.list.itemHeights[i]; ok {
			height = h
		}

		if rowOffset <= l.list.offsetY-height-padding {
			// before scroll
		} else if rowOffset <= l.list.offsetY {
			minRow = i
			offY = rowOffset
			isVisible = true
		}
		if rowOffset >= l.list.offsetY+float64(l.list.scroller.Size().Height) {
			break
		}

		rowOffset += height + padding
		if isVisible {
			l.visibleRowHeights = append(l.visibleRowHeights, height)
		}
	}
	return
}

// Declare conformity with WidgetRenderer interface.
var _ fyne.WidgetRenderer = (*listRenderer)(nil)

type listRenderer struct {
	objects []fyne.CanvasObject

	list     *List
	scroller *container.Scroll
	layout   *fyne.Container
}

// Implements: fyne.WidgetRenderer
func (lr *listRenderer) Destroy() {}

// Implements: fyne.WidgetRenderer
func (lr *listRenderer) Objects() []fyne.CanvasObject {
	return lr.objects
}

func newListRenderer(objects []fyne.CanvasObject, l *List, scroller *container.Scroll, layout *fyne.Container) *listRenderer {
	lr := &listRenderer{objects: objects, list: l, scroller: scroller, layout: layout}
	lr.scroller.OnScrolled = l.offsetUpdated
	return lr
}

func (lr *listRenderer) Layout(size fyne.Size) {
	lr.scroller.Resize(size)
}

func (lr *listRenderer) MinSize() fyne.Size {
	return lr.scroller.MinSize().Max(lr.list.itemMin)
}

func (lr *listRenderer) Refresh() {
	if f := lr.list.CreateItem; f != nil {
		item := createItemAndApplyThemeScope(f)
		lr.list.itemMin = item.MinSize()
	}
	lr.Layout(lr.list.Size())
	lr.scroller.Refresh()
	layout := lr.layout.Layout.(*listLayout)
	layout.updateList(false)

	for _, s := range layout.separators {
		s.Refresh()
	}
	//canvas.Refresh(l.list.super())
	canvas.Refresh(lr.list)
}

// Declare conformity with interfaces.
var _ fyne.Widget = (*listItem)(nil)
var _ fyne.Tappable = (*listItem)(nil)
var _ fyne.SecondaryTappable = (*listItem)(nil)

type listItem struct {
	widget.BaseWidget

	onTapped          func(*fyne.PointEvent)
	onSecondaryTapped func(*fyne.PointEvent)
	child             fyne.CanvasObject
}

func newListItem(child fyne.CanvasObject, tapped, secondaryTapped func(*fyne.PointEvent)) *listItem {
	li := &listItem{
		child:             child,
		onTapped:          tapped,
		onSecondaryTapped: secondaryTapped,
	}

	li.ExtendBaseWidget(li)
	return li
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (li *listItem) CreateRenderer() fyne.WidgetRenderer {
	li.ExtendBaseWidget(li)

	return &listItemRenderer{object: li.child, item: li}
}

// MinSize returns the size that this widget should not shrink below.
func (li *listItem) MinSize() fyne.Size {
	li.ExtendBaseWidget(li)
	return li.BaseWidget.MinSize()
}

// Tapped is called when a pointer tapped event is captured and triggers any tap handler.
func (li *listItem) Tapped(pe *fyne.PointEvent) {
	if li.onTapped != nil {
		li.onTapped(pe)
	}
}

func (li *listItem) TappedSecondary(pe *fyne.PointEvent) {
	if li.onSecondaryTapped != nil {
		li.onSecondaryTapped(pe)
	}
}

// Declare conformity with the WidgetRenderer interface.
var _ fyne.WidgetRenderer = (*listItemRenderer)(nil)

type listItemRenderer struct {
	object fyne.CanvasObject

	item *listItem
}

func (li *listItemRenderer) Destroy() {}

func (li *listItemRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{li.object}
}

// MinSize calculates the minimum size of a listItem.
// This is based on the size of the status indicator and the size of the child object.
func (li *listItemRenderer) MinSize() fyne.Size {
	return li.item.child.MinSize()
}

// Layout the components of the listItem widget.
func (li *listItemRenderer) Layout(size fyne.Size) {
	li.item.child.Resize(size)
}

func (li *listItemRenderer) Refresh() {
	canvas.Refresh(li.item)
}

// Declare conformity with Layout interface.
var _ fyne.Layout = (*listLayout)(nil)

type listItemAndID struct {
	item *listItem
	id   ListItemID
}

type listLayout struct {
	list       *List
	separators []fyne.CanvasObject
	children   []fyne.CanvasObject

	itemPool          Pool[fyne.CanvasObject]
	visible           []listItemAndID
	wasVisible        []listItemAndID
	visibleRowHeights []float64
}

func newListLayout(list *List) fyne.Layout {
	l := &listLayout{list: list}
	list.offsetUpdated = l.offsetUpdated
	return l
}

func (l *listLayout) Layout([]fyne.CanvasObject, fyne.Size) {
	l.updateList(true)
}

func (l *listLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return l.list.contentMinSize()
}

func (l *listLayout) getItem() *listItem {
	item := l.itemPool.Get()
	if item == nil {
		if f := l.list.CreateItem; f != nil {
			item2 := createItemAndApplyThemeScope(f)

			item = newListItem(item2, nil, nil)
		}
	}
	return item.(*listItem)
}

func (l *listLayout) offsetUpdated(pos fyne.Position) {
	if l.list.offsetY == float64(pos.Y) {
		return
	}
	l.list.offsetY = float64(pos.Y)
	l.updateList(true)
}

func (l *listLayout) setupListItem(li *listItem, id ListItemID) {
	if f := l.list.UpdateItem; f != nil {
		f(id, li.child)
	}

	li.onTapped = func(pe *fyne.PointEvent) {
		if f := l.list.OnItemTapped; f != nil {
			l.list.OnItemTapped(id, pe)
		}
	}
	li.onSecondaryTapped = func(pe *fyne.PointEvent) {
		if f := l.list.OnItemSecondaryTapped; f != nil {
			l.list.OnItemSecondaryTapped(id, pe)
		}
	}
}

func (l *listLayout) updateList(newOnly bool) {
	th := l.list.Theme()
	separatorThickness := float64(th.Size(theme.SizeNamePadding))
	width := l.list.Size().Width
	length := 0
	if f := l.list.Length; f != nil {
		length = f()
	}
	if l.list.UpdateItem == nil {
		fyne.LogError("Missing UpdateCell callback required for List", nil)
	}

	// l.wasVisible now represents the currently visible items, while
	// l.visible will be updated to represent what is visible *after* the update
	l.wasVisible = append(l.wasVisible, l.visible...)
	l.visible = l.visible[:0]

	offY, minRow := l.calculateVisibleRowHeights(float64(l.list.itemMin.Height), length, th)
	if len(l.visibleRowHeights) == 0 && length > 0 { // we can't show anything until we have some dimensions
		return
	}

	oldChildrenLen := len(l.children)
	l.children = l.children[:0]

	y := offY
	for index, itemHeight := range l.visibleRowHeights {
		row := index + minRow
		size := fyne.NewSize(width, float32(itemHeight))

		c, ok := l.searchVisible(l.wasVisible, row)
		if !ok {
			c = l.getItem()
			if c == nil {
				continue
			}
			c.Resize(size)
		}

		c.Move(fyne.NewPos(0, float32(y)))
		c.Resize(size)

		y += itemHeight + separatorThickness
		l.visible = append(l.visible, listItemAndID{id: row, item: c})
		l.children = append(l.children, c)
	}
	l.nilOldSliceData(l.children, len(l.children), oldChildrenLen)

	for _, wasVis := range l.wasVisible {
		if _, ok := l.searchVisible(l.visible, wasVis.id); !ok {
			l.itemPool.Put(wasVis.item)
		}
	}

	l.updateSeparators()

	c := l.list.scroller.Content.(*fyne.Container)
	oldObjLen := len(c.Objects)
	c.Objects = c.Objects[:0]
	c.Objects = append(c.Objects, l.children...)
	c.Objects = append(c.Objects, l.separators...)
	l.nilOldSliceData(c.Objects, len(c.Objects), oldObjLen)

	if newOnly {
		for _, vis := range l.visible {
			if _, ok := l.searchVisible(l.wasVisible, vis.id); !ok {
				l.setupListItem(vis.item, vis.id)
			}
		}
	} else {
		for _, vis := range l.visible {
			l.setupListItem(vis.item, vis.id)
		}

		// a full refresh may change theme, we should drain the pool of unused items instead of refreshing them.
		for l.itemPool.Get() != nil {
		}
	}

	// we don't need wasVisible now until next call to update
	// nil out all references before truncating slice
	//for i := 0; i < len(l.wasVisible); i++ {
	// modernised
	for i := range len(l.wasVisible) {
		l.wasVisible[i].item = nil
	}
	l.wasVisible = l.wasVisible[:0]
}

func (l *listLayout) updateSeparators() {
	if l.list.HideSeparators {
		l.separators = nil
		return
	}
	if lenChildren := len(l.children); lenChildren > 1 {
		if lenSep := len(l.separators); lenSep > lenChildren {
			l.separators = l.separators[:lenChildren]
		} else {
			for i := lenSep; i < lenChildren; i++ {
				l.separators = append(l.separators, widget.NewSeparator())
			}
		}
	} else {
		l.separators = nil
	}

	th := l.list.Theme()
	separatorThickness := th.Size(theme.SizeNameSeparatorThickness)
	dividerOff := (th.Size(theme.SizeNamePadding) + separatorThickness) / 2
	for i, child := range l.children {
		if i == 0 {
			continue
		}
		l.separators[i].Move(fyne.NewPos(0, child.Position().Y-dividerOff))
		l.separators[i].Resize(fyne.NewSize(l.list.Size().Width, separatorThickness))
		l.separators[i].Show()
	}
}

// invariant: visible is in ascending order of IDs
func (l *listLayout) searchVisible(visible []listItemAndID, id ListItemID) (*listItem, bool) {
	ln := len(visible)
	idx := sort.Search(ln, func(i int) bool { return visible[i].id >= id })
	if idx < ln && visible[idx].id == id {
		return visible[idx].item, true
	}
	return nil, false
}

func (l *listLayout) nilOldSliceData(objs []fyne.CanvasObject, len, oldLen int) {
	if oldLen > len {
		objs = objs[:oldLen] // gain view into old data
		for i := len; i < oldLen; i++ {
			objs[i] = nil
		}
	}
}

func createItemAndApplyThemeScope(f func() fyne.CanvasObject) fyne.CanvasObject {
	item := f()
	item.Refresh()
	return item
}

// Pool is the generic version of sync.Pool.
type Pool[T any] struct {
	pool sync.Pool

	// New specifies a function to generate
	// a value when Get would otherwise return the zero value of T.
	New func() T
}

// Get selects an arbitrary item from the Pool, removes it from the Pool,
// and returns it to the caller.
func (p *Pool[T]) Get() T {
	x, ok := p.pool.Get().(T)
	if !ok && p.New != nil {
		return p.New()
	}

	return x
}

// Put adds x to the pool.
func (p *Pool[T]) Put(x T) {
	p.pool.Put(x)
}
