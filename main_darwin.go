//go:build darwin

package main

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
*/
import "C"

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type KeyEntry struct {
	Key        string
	IntervalMS int
	Enabled    bool
}

type KeyTask struct {
	KeyCode     C.CGKeyCode
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

	ticker := time.NewTicker(task.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			if task.UseUnicode {
				keyTapUnicode(task.UnicodeRune)
			} else {
				keyTap(task.KeyCode)
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
	entries := []*KeyEntry{
		{Key: "A", IntervalMS: 1000, Enabled: true},
	}

	application := app.New()
	window := application.NewWindow("Auto Key Presser")
	window.Resize(fyne.NewSize(520, 360))

	statusLabel := widget.NewLabel("Status: idle")
	runner := &Runner{}

	selectedIndex := -1
	var startButton *widget.Button
	var stopButton *widget.Button

	list := widget.NewList(
		func() int { return len(entries) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i int, o fyne.CanvasObject) {
			entry := entries[i]
			label := o.(*widget.Label)
			label.SetText(fmt.Sprintf("%s - %d ms - %s", entry.Key, entry.IntervalMS, enabledLabel(entry.Enabled)))
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		selectedIndex = id
	}
	list.OnUnselected = func(id widget.ListItemID) {
		if selectedIndex == id {
			selectedIndex = -1
		}
	}

	addButton := widget.NewButton("Add", func() {
		showAddDialog(window, func(entry *KeyEntry) {
			entries = append(entries, entry)
			list.Refresh()
		})
	})

	removeButton := widget.NewButton("Remove", func() {
		if selectedIndex < 0 || selectedIndex >= len(entries) {
			dialog.ShowInformation("Remove", "Select a row to remove.", window)
			return
		}
		entries = append(entries[:selectedIndex], entries[selectedIndex+1:]...)
		selectedIndex = -1
		list.UnselectAll()
		list.Refresh()
	})

	startButton = widget.NewButton("Start", func() {
		if runner.IsRunning() {
			return
		}

		var tasks []KeyTask
		var errors []string
		for _, entry := range entries {
			if !entry.Enabled || entry.IntervalMS <= 0 || strings.TrimSpace(entry.Key) == "" {
				continue
			}
			task, err := parseMacInput(entry.Key)
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
			dialog.ShowInformation("Start", "Add at least one enabled key with a positive interval.", window)
			if len(errors) > 0 {
				dialog.ShowInformation("Key errors", strings.Join(errors, "\n"), window)
			}
			return
		}

		if len(errors) > 0 {
			dialog.ShowInformation("Some keys were skipped", strings.Join(errors, "\n"), window)
		}

		runner.Start(tasks)
		setRunningStateMac(true, statusLabel, addButton, removeButton, startButton, stopButton)
	})

	stopButton = widget.NewButton("Stop", func() {
		runner.Stop()
		setRunningStateMac(false, statusLabel, addButton, removeButton, startButton, stopButton)
	})
	stopButton.Disable()

	controls := container.NewHBox(addButton, removeButton, startButton, stopButton)
	content := container.NewBorder(controls, statusLabel, nil, nil, list)
	window.SetContent(content)

	window.ShowAndRun()
}

func setRunningStateMac(running bool, statusLabel *widget.Label, addButton, removeButton, startButton, stopButton *widget.Button) {
	if running {
		statusLabel.SetText("Status: running")
		addButton.Disable()
		removeButton.Disable()
		startButton.Disable()
		stopButton.Enable()
	} else {
		statusLabel.SetText("Status: idle")
		addButton.Enable()
		removeButton.Enable()
		startButton.Enable()
		stopButton.Disable()
	}
}

func showAddDialog(window fyne.Window, onAdd func(*KeyEntry)) {
	keyEntry := widget.NewEntry()
	intervalEntry := widget.NewEntry()
	intervalEntry.SetText("1000")
	enabledCheck := widget.NewCheck("Enabled", nil)
	enabledCheck.SetChecked(true)

	form := dialog.NewForm("Add Key", "Add", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Key (ex: A, F5, SPACE)", keyEntry),
			widget.NewFormItem("Interval (ms)", intervalEntry),
			widget.NewFormItem("", enabledCheck),
		},
		func(ok bool) {
			if !ok {
				return
			}
			key := strings.TrimSpace(keyEntry.Text)
			interval := parseInterval(intervalEntry.Text)
			if key == "" || interval <= 0 {
				dialog.ShowInformation("Validation", "Enter a key and a positive interval in ms.", window)
				return
			}
			onAdd(&KeyEntry{
				Key:        key,
				IntervalMS: interval,
				Enabled:    enabledCheck.Checked,
			})
		},
		window,
	)
	form.Show()
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
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

func parseMacInput(input string) (KeyTask, error) {
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
		ch := key[0]
		if code, ok := macLetterKeyCode(ch); ok {
			return KeyTask{KeyCode: code}, nil
		}
		if code, ok := macDigitKeyCode(ch); ok {
			return KeyTask{KeyCode: code}, nil
		}
	}

	switch key {
	case "SPACE":
		return KeyTask{KeyCode: 49}, nil
	case "ENTER":
		return KeyTask{KeyCode: 36}, nil
	case "ESC", "ESCAPE":
		return KeyTask{KeyCode: 53}, nil
	case "TAB":
		return KeyTask{KeyCode: 48}, nil
	case "UP":
		return KeyTask{KeyCode: 126}, nil
	case "DOWN":
		return KeyTask{KeyCode: 125}, nil
	case "LEFT":
		return KeyTask{KeyCode: 123}, nil
	case "RIGHT":
		return KeyTask{KeyCode: 124}, nil
	case "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12":
		code, err := macFunctionKeyCode(key)
		if err != nil {
			return KeyTask{}, err
		}
		return KeyTask{KeyCode: code}, nil
	default:
		return KeyTask{}, fmt.Errorf("unsupported key: %s", input)
	}
}

func keyTap(code C.CGKeyCode) {
	eventDown := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), code, C.bool(true))
	eventUp := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), code, C.bool(false))
	if eventDown == C.CGEventRef(0) || eventUp == C.CGEventRef(0) {
		return
	}
	C.CGEventPost(C.kCGHIDEventTap, eventDown)
	C.CGEventPost(C.kCGHIDEventTap, eventUp)
	C.CFRelease(C.CFTypeRef(eventDown))
	C.CFRelease(C.CFTypeRef(eventUp))
}

func keyTapUnicode(r rune) {
	units := utf16.Encode([]rune{r})
	if len(units) == 0 {
		return
	}

	eventDown := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), 0, C.bool(true))
	eventUp := C.CGEventCreateKeyboardEvent(C.CGEventSourceRef(0), 0, C.bool(false))
	if eventDown == C.CGEventRef(0) || eventUp == C.CGEventRef(0) {
		return
	}

	C.CGEventKeyboardSetUnicodeString(
		eventDown,
		C.UniCharCount(len(units)),
		(*C.UniChar)(unsafe.Pointer(&units[0])),
	)
	C.CGEventKeyboardSetUnicodeString(
		eventUp,
		C.UniCharCount(len(units)),
		(*C.UniChar)(unsafe.Pointer(&units[0])),
	)

	C.CGEventPost(C.kCGHIDEventTap, eventDown)
	C.CGEventPost(C.kCGHIDEventTap, eventUp)
	C.CFRelease(C.CFTypeRef(eventDown))
	C.CFRelease(C.CFTypeRef(eventUp))
}

func macLetterKeyCode(ch byte) (C.CGKeyCode, bool) {
	switch ch {
	case 'A':
		return 0, true
	case 'B':
		return 11, true
	case 'C':
		return 8, true
	case 'D':
		return 2, true
	case 'E':
		return 14, true
	case 'F':
		return 3, true
	case 'G':
		return 5, true
	case 'H':
		return 4, true
	case 'I':
		return 34, true
	case 'J':
		return 38, true
	case 'K':
		return 40, true
	case 'L':
		return 37, true
	case 'M':
		return 46, true
	case 'N':
		return 45, true
	case 'O':
		return 31, true
	case 'P':
		return 35, true
	case 'Q':
		return 12, true
	case 'R':
		return 15, true
	case 'S':
		return 1, true
	case 'T':
		return 17, true
	case 'U':
		return 32, true
	case 'V':
		return 9, true
	case 'W':
		return 13, true
	case 'X':
		return 7, true
	case 'Y':
		return 16, true
	case 'Z':
		return 6, true
	default:
		return 0, false
	}
}

func macDigitKeyCode(ch byte) (C.CGKeyCode, bool) {
	switch ch {
	case '0':
		return 29, true
	case '1':
		return 18, true
	case '2':
		return 19, true
	case '3':
		return 20, true
	case '4':
		return 21, true
	case '5':
		return 23, true
	case '6':
		return 22, true
	case '7':
		return 26, true
	case '8':
		return 28, true
	case '9':
		return 25, true
	default:
		return 0, false
	}
}

func macFunctionKeyCode(key string) (C.CGKeyCode, error) {
	switch key {
	case "F1":
		return 122, nil
	case "F2":
		return 120, nil
	case "F3":
		return 99, nil
	case "F4":
		return 118, nil
	case "F5":
		return 96, nil
	case "F6":
		return 97, nil
	case "F7":
		return 98, nil
	case "F8":
		return 100, nil
	case "F9":
		return 101, nil
	case "F10":
		return 109, nil
	case "F11":
		return 103, nil
	case "F12":
		return 111, nil
	default:
		return 0, fmt.Errorf("unsupported key: %s", key)
	}
}
