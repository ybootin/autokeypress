//go:build windows

package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/micmonay/keybd_event"
)

type KeyEntry struct {
	Key        string
	IntervalMS int
	Enabled    bool
}

type KeyTableModel struct {
	walk.TableModelBase
	items []*KeyEntry
}

func (m *KeyTableModel) RowCount() int {
	return len(m.items)
}

func (m *KeyTableModel) Value(row, col int) interface{} {
	entry := m.items[row]
	switch col {
	case 0:
		return entry.Key
	case 1:
		return entry.IntervalMS
	case 2:
		return entry.Enabled
	default:
		return ""
	}
}

func (m *KeyTableModel) SetValue(row, col int, value interface{}) error {
	entry := m.items[row]
	switch col {
	case 0:
		entry.Key = strings.TrimSpace(fmt.Sprintf("%v", value))
	case 1:
		entry.IntervalMS = parseInterval(value)
	case 2:
		switch v := value.(type) {
		case bool:
			entry.Enabled = v
		default:
			entry.Enabled = strings.EqualFold(fmt.Sprintf("%v", value), "true")
		}
	default:
		return nil
	}

	m.PublishRowChanged(row)
	return nil
}

func (m *KeyTableModel) Add(entry *KeyEntry) {
	m.items = append(m.items, entry)
	m.PublishRowsInserted(len(m.items)-1, len(m.items)-1)
}

func (m *KeyTableModel) Remove(index int) {
	if index < 0 || index >= len(m.items) {
		return
	}
	m.items = append(m.items[:index], m.items[index+1:]...)
	m.PublishRowsRemoved(index, index)
}

func (m *KeyTableModel) EnabledEntries() []*KeyEntry {
	var entries []*KeyEntry
	for _, entry := range m.items {
		if entry.Enabled && entry.IntervalMS > 0 && strings.TrimSpace(entry.Key) != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

type KeyTask struct {
	KeyCode     int
	UnicodeRune rune
	UseUnicode  bool
	Interval    time.Duration
}

type Runner struct {
	mu      sync.Mutex
	stopCh  chan struct{}
	wg      sync.WaitGroup
	running bool
}

func (r *Runner) Start(tasks []KeyTask) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.stopCh = make(chan struct{})
	r.mu.Unlock()

	for _, task := range tasks {
		r.wg.Add(1)
		go r.runTask(task)
	}
}

func (r *Runner) runTask(task KeyTask) {
	defer r.wg.Done()

	var kb keybd_event.KeyBonding
	if !task.UseUnicode {
		var err error
		kb, err = keybd_event.NewKeyBonding()
		if err != nil {
			return
		}
		kb.SetKeys(task.KeyCode)
	}

	ticker := time.NewTicker(task.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			if task.UseUnicode {
				sendUnicode(task.UnicodeRune)
			} else {
				_ = kb.Launching()
			}
		}
	}
}

func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	close(r.stopCh)
	r.running = false
	r.mu.Unlock()

	r.wg.Wait()
}

func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func main() {
	var (
		mainWindow   *walk.MainWindow
		tableView    *walk.TableView
		statusLabel  *walk.Label
		addButton    *walk.PushButton
		removeButton *walk.PushButton
		startButton  *walk.PushButton
		stopButton   *walk.PushButton
	)

	model := &KeyTableModel{
		items: []*KeyEntry{
			{Key: "A", IntervalMS: 1000, Enabled: true},
		},
	}
	runner := &Runner{}

	MainWindow{
		AssignTo: &mainWindow,
		Title:    "Auto Key Presser",
		MinSize:  Size{Width: 520, Height: 360},
		Layout:   VBox{},
		Children: []Widget{
			TableView{
				AssignTo: &tableView,
				Model:    model,
				Columns: []TableViewColumn{
					{Title: "Key", Width: 120},
					{Title: "Interval (ms)", Width: 120},
					{Title: "Enabled", Width: 80, CheckBoxes: true},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{
						AssignTo: &addButton,
						Text:     "Add",
						OnClicked: func() {
							entry, ok := showAddDialog(mainWindow)
							if !ok {
								return
							}
							model.Add(entry)
						},
					},
					PushButton{
						AssignTo: &removeButton,
						Text:     "Remove",
						OnClicked: func() {
							index := tableView.CurrentIndex()
							if index < 0 {
								_ = walk.MsgBox(mainWindow, "Remove", "Select a row to remove.", walk.MsgBoxIconInformation)
								return
							}
							model.Remove(index)
						},
					},
					PushButton{
						AssignTo: &startButton,
						Text:     "Start",
						OnClicked: func() {
							if runner.IsRunning() {
								return
							}

							entries := model.EnabledEntries()
							if len(entries) == 0 {
								_ = walk.MsgBox(mainWindow, "Start", "Add at least one enabled key with a positive interval.", walk.MsgBoxIconWarning)
								return
							}

							var tasks []KeyTask
							var errors []string
							for _, entry := range entries {
								task, err := parseKeyInput(entry.Key)
								if err != nil {
									errors = append(errors, err.Error())
									continue
								}
								tasks = append(tasks, KeyTask{
									KeyCode:     task.KeyCode,
									UnicodeRune: task.UnicodeRune,
									UseUnicode:  task.UseUnicode,
									Interval:    time.Duration(entry.IntervalMS) * time.Millisecond,
								})
							}

							if len(tasks) == 0 {
								_ = walk.MsgBox(mainWindow, "Start", strings.Join(errors, "\n"), walk.MsgBoxIconWarning)
								return
							}

							if len(errors) > 0 {
								_ = walk.MsgBox(mainWindow, "Some keys were skipped", strings.Join(errors, "\n"), walk.MsgBoxIconWarning)
							}

							runner.Start(tasks)
							setRunningState(true, addButton, removeButton, startButton, stopButton, statusLabel)
						},
					},
					PushButton{
						AssignTo: &stopButton,
						Text:     "Stop",
						Enabled:  false,
						OnClicked: func() {
							runner.Stop()
							setRunningState(false, addButton, removeButton, startButton, stopButton, statusLabel)
						},
					},
				},
			},
			Label{
				AssignTo: &statusLabel,
				Text:     "Status: idle",
			},
		},
	}.Run()
}

func setRunningState(running bool, addButton, removeButton, startButton, stopButton *walk.PushButton, statusLabel *walk.Label) {
	addButton.SetEnabled(!running)
	removeButton.SetEnabled(!running)
	startButton.SetEnabled(!running)
	stopButton.SetEnabled(running)
	if running {
		statusLabel.SetText("Status: running")
	} else {
		statusLabel.SetText("Status: idle")
	}
}

func showAddDialog(owner walk.Form) (*KeyEntry, bool) {
	var (
		dlg        *walk.Dialog
		keyEdit    *walk.LineEdit
		intervalEd *walk.LineEdit
		enabledCb  *walk.CheckBox
	)

	accepted := false

	Dialog{
		AssignTo: &dlg,
		Title:    "Add Key",
		Layout:   VBox{},
		MinSize:  Size{Width: 300, Height: 160},
		Children: []Widget{
			Label{Text: "Key (ex: A, F5, SPACE):"},
			LineEdit{AssignTo: &keyEdit},
			Label{Text: "Interval (ms):"},
			LineEdit{AssignTo: &intervalEd, Text: "1000"},
			CheckBox{AssignTo: &enabledCb, Text: "Enabled", Checked: true},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{
						Text: "Add",
						OnClicked: func() {
							key := strings.TrimSpace(keyEdit.Text())
							interval := parseInterval(intervalEd.Text())
							if key == "" || interval <= 0 {
								_ = walk.MsgBox(dlg, "Validation", "Enter a key and a positive interval in ms.", walk.MsgBoxIconWarning)
								return
							}

							accepted = true
							dlg.Accept()
						},
					},
					PushButton{
						Text: "Cancel",
						OnClicked: func() {
							dlg.Cancel()
						},
					},
				},
			},
		},
	}.Run(owner)

	if !accepted {
		return nil, false
	}

	return &KeyEntry{
		Key:        strings.TrimSpace(keyEdit.Text()),
		IntervalMS: parseInterval(intervalEd.Text()),
		Enabled:    enabledCb.Checked(),
	}, true
}

func parseInterval(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		value := strings.TrimSpace(v)
		if value == "" {
			return 0
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func parseKeyInput(input string) (KeyTask, error) {
	key := strings.ToUpper(strings.TrimSpace(input))
	if key == "" {
		return KeyTask{}, fmt.Errorf("empty key")
	}

	runes := []rune(strings.TrimSpace(input))
	if len(runes) == 1 {
		return KeyTask{
			UnicodeRune: runes[0],
			UseUnicode:  true,
		}, nil
	}

	if len(key) == 1 {
		switch key[0] {
		case 'A':
			return KeyTask{KeyCode: keybd_event.VK_A}, nil
		case 'B':
			return KeyTask{KeyCode: keybd_event.VK_B}, nil
		case 'C':
			return KeyTask{KeyCode: keybd_event.VK_C}, nil
		case 'D':
			return KeyTask{KeyCode: keybd_event.VK_D}, nil
		case 'E':
			return KeyTask{KeyCode: keybd_event.VK_E}, nil
		case 'F':
			return KeyTask{KeyCode: keybd_event.VK_F}, nil
		case 'G':
			return KeyTask{KeyCode: keybd_event.VK_G}, nil
		case 'H':
			return KeyTask{KeyCode: keybd_event.VK_H}, nil
		case 'I':
			return KeyTask{KeyCode: keybd_event.VK_I}, nil
		case 'J':
			return KeyTask{KeyCode: keybd_event.VK_J}, nil
		case 'K':
			return KeyTask{KeyCode: keybd_event.VK_K}, nil
		case 'L':
			return KeyTask{KeyCode: keybd_event.VK_L}, nil
		case 'M':
			return KeyTask{KeyCode: keybd_event.VK_M}, nil
		case 'N':
			return KeyTask{KeyCode: keybd_event.VK_N}, nil
		case 'O':
			return KeyTask{KeyCode: keybd_event.VK_O}, nil
		case 'P':
			return KeyTask{KeyCode: keybd_event.VK_P}, nil
		case 'Q':
			return KeyTask{KeyCode: keybd_event.VK_Q}, nil
		case 'R':
			return KeyTask{KeyCode: keybd_event.VK_R}, nil
		case 'S':
			return KeyTask{KeyCode: keybd_event.VK_S}, nil
		case 'T':
			return KeyTask{KeyCode: keybd_event.VK_T}, nil
		case 'U':
			return KeyTask{KeyCode: keybd_event.VK_U}, nil
		case 'V':
			return KeyTask{KeyCode: keybd_event.VK_V}, nil
		case 'W':
			return KeyTask{KeyCode: keybd_event.VK_W}, nil
		case 'X':
			return KeyTask{KeyCode: keybd_event.VK_X}, nil
		case 'Y':
			return KeyTask{KeyCode: keybd_event.VK_Y}, nil
		case 'Z':
			return KeyTask{KeyCode: keybd_event.VK_Z}, nil
		case '0':
			return KeyTask{KeyCode: keybd_event.VK_0}, nil
		case '1':
			return KeyTask{KeyCode: keybd_event.VK_1}, nil
		case '2':
			return KeyTask{KeyCode: keybd_event.VK_2}, nil
		case '3':
			return KeyTask{KeyCode: keybd_event.VK_3}, nil
		case '4':
			return KeyTask{KeyCode: keybd_event.VK_4}, nil
		case '5':
			return KeyTask{KeyCode: keybd_event.VK_5}, nil
		case '6':
			return KeyTask{KeyCode: keybd_event.VK_6}, nil
		case '7':
			return KeyTask{KeyCode: keybd_event.VK_7}, nil
		case '8':
			return KeyTask{KeyCode: keybd_event.VK_8}, nil
		case '9':
			return KeyTask{KeyCode: keybd_event.VK_9}, nil
		}
	}

	switch key {
	case "SPACE":
		return KeyTask{KeyCode: keybd_event.VK_SPACE}, nil
	case "ENTER":
		return KeyTask{KeyCode: keybd_event.VK_ENTER}, nil
	case "ESC", "ESCAPE":
		return KeyTask{KeyCode: keybd_event.VK_ESC}, nil
	case "TAB":
		return KeyTask{KeyCode: keybd_event.VK_TAB}, nil
	case "UP":
		return KeyTask{KeyCode: keybd_event.VK_UP}, nil
	case "DOWN":
		return KeyTask{KeyCode: keybd_event.VK_DOWN}, nil
	case "LEFT":
		return KeyTask{KeyCode: keybd_event.VK_LEFT}, nil
	case "RIGHT":
		return KeyTask{KeyCode: keybd_event.VK_RIGHT}, nil
	case "F1":
		return KeyTask{KeyCode: keybd_event.VK_F1}, nil
	case "F2":
		return KeyTask{KeyCode: keybd_event.VK_F2}, nil
	case "F3":
		return KeyTask{KeyCode: keybd_event.VK_F3}, nil
	case "F4":
		return KeyTask{KeyCode: keybd_event.VK_F4}, nil
	case "F5":
		return KeyTask{KeyCode: keybd_event.VK_F5}, nil
	case "F6":
		return KeyTask{KeyCode: keybd_event.VK_F6}, nil
	case "F7":
		return KeyTask{KeyCode: keybd_event.VK_F7}, nil
	case "F8":
		return KeyTask{KeyCode: keybd_event.VK_F8}, nil
	case "F9":
		return KeyTask{KeyCode: keybd_event.VK_F9}, nil
	case "F10":
		return KeyTask{KeyCode: keybd_event.VK_F10}, nil
	case "F11":
		return KeyTask{KeyCode: keybd_event.VK_F11}, nil
	case "F12":
		return KeyTask{KeyCode: keybd_event.VK_F12}, nil
	default:
		return KeyTask{}, fmt.Errorf("unsupported key: %s", input)
	}
}

const (
	inputKeyboard    = 1
	keyeventfUnicode = 0x0004
	keyeventfKeyUp   = 0x0002
)

type keyboardInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type input struct {
	Type uint32
	Ki   keyboardInput
	_    uint64
}

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

func sendUnicode(r rune) {
	units := utf16.Encode([]rune{r})
	for _, unit := range units {
		sendUnicodeUnit(uint16(unit), 0)
		sendUnicodeUnit(uint16(unit), keyeventfKeyUp)
	}
}

func sendUnicodeUnit(scan uint16, flags uint32) {
	in := input{
		Type: inputKeyboard,
		Ki: keyboardInput{
			Scan:  scan,
			Flags: keyeventfUnicode | flags,
		},
	}
	procSendInput.Call(
		1,
		uintptr(unsafe.Pointer(&in)),
		unsafe.Sizeof(in),
	)
}
