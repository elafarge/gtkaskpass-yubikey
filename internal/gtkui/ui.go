// SPDX-License-Identifier: Apache-2.0
package gtkui

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gdkx11/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/elafarge/gtkaskpass-yubikey/internal/askpass"
	"github.com/elafarge/gtkaskpass-yubikey/internal/cacheipc"
	"github.com/elafarge/gtkaskpass-yubikey/internal/dialog"
)

// Run renders service-owned views. This worker has no cache or device access.
// Call on the initial locked OS thread with a private inherited IPC connection.
func Run(parent context.Context, c net.Conn) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	views := make(chan dialog.View, 1)
	go func() {
		defer cancel()
		for {
			var v dialog.View
			if cacheipc.ReadFrame(c, &v) != nil {
				return
			}
			select {
			case views <- v:
			case <-ctx.Done():
				return
			}
			if v.Stage == "close" {
				return
			}
		}
	}()
	var initial dialog.View
	select {
	case initial = <-views:
	case <-ctx.Done():
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("UI handshake timed out")
	}
	if !gtk.InitCheck() {
		return errors.New("cannot connect to a graphical display")
	}
	applicationID := "io.github.gtkaskpass_yubikey"
	if initial.PassiveTouch {
		applicationID += ".touch"
	}
	a := gtk.NewApplication(applicationID, gio.ApplicationNonUnique)
	var win *gtk.ApplicationWindow
	var entry *gtk.PasswordEntry
	var current dialog.View
	var nextRefresh time.Time
	var render func(dialog.View)
	send := func(action dialog.Action) {
		if err := c.SetWriteDeadline(time.Now().Add(cacheipc.Timeout)); err != nil {
			cancel()
			return
		}
		if err := cacheipc.WriteFrame(c, action); err != nil {
			cancel()
		}
	}
	dismiss := func() {
		if current.Request.Mode == askpass.Notify {
			win.SetVisible(false)
		} else {
			send(dialog.Action{Kind: "cancel"})
			win.SetSensitive(false)
		}
	}
	render = func(v dialog.View) {
		if entry != nil {
			entry.SetText("")
			entry = nil
		}
		if v.Stage == "close" {
			a.Quit()
			return
		}
		current = v
		nextRefresh = time.Now().Add(time.Second)
		win.SetTitle(v.Request.Title)
		win.SetDefaultSize(480, -1)
		box := gtk.NewBox(gtk.OrientationVertical, 12)
		box.SetMarginTop(20)
		box.SetMarginBottom(20)
		box.SetMarginStart(20)
		box.SetMarginEnd(20)
		win.SetChild(box)
		prompt := gtk.NewLabel(v.Request.Prompt)
		prompt.SetWrap(true)
		prompt.SetXAlign(0)
		prompt.SetMaxWidthChars(64)
		prompt.SetSelectable(true)
		scroll := gtk.NewScrolledWindow()
		scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
		scroll.SetPropagateNaturalHeight(true)
		scroll.SetMaxContentHeight(250)
		scroll.SetChild(prompt)
		box.Append(scroll)
		if v.Message != "" {
			label := gtk.NewLabel(v.Message)
			label.SetWrap(true)
			label.SetMaxWidthChars(64)
			label.SetXAlign(0)
			box.Append(label)
		}
		buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
		buttons.SetHAlign(gtk.AlignEnd)
		button := func(label string, f func()) *gtk.Button {
			b := gtk.NewButtonWithMnemonic(label)
			b.ConnectClicked(f)
			buttons.Append(b)
			return b
		}
		switch {
		case v.Request.Mode == askpass.Notify:
			spinner := gtk.NewSpinner()
			spinner.Start()
			box.Append(spinner)
			text := "Waiting for SSH. Dismiss hides this notification without cancelling signing."
			if v.PassiveTouch {
				text = "Dismiss hides this notification without cancelling the device operation."
			}
			label := gtk.NewLabel(text)
			label.SetWrap(true)
			label.SetMaxWidthChars(60)
			box.Append(label)
			button("_Dismiss", dismiss)
		case v.Request.Mode == askpass.Confirm:
			win.SetDefaultWidget(button("_Deny", dismiss))
			button("_Allow", func() { send(dialog.Action{Kind: "submit"}); win.SetSensitive(false) })
		case v.Stage == "choose":
			labels := make([]string, len(v.Devices))
			for i, d := range v.Devices {
				labels[i] = d.Label
			}
			chooser := gtk.NewDropDownFromStrings(labels)
			chooser.SetSelected(uint(v.Selected))
			box.Append(chooser)
			button("_Cancel", dismiss)
			button("_Refresh", func() { send(dialog.Action{Kind: "refresh"}); win.SetSensitive(false) })
			button("_Use device", func() {
				i := int(chooser.Selected())
				if i >= 0 && i < len(v.Devices) {
					send(dialog.Action{Kind: "select", Selected: i})
					win.SetSensitive(false)
				}
			})
		case v.Stage == "wait":
			button("_Cancel", dismiss)
			button("_Refresh", func() { send(dialog.Action{Kind: "refresh"}); win.SetSensitive(false) })
		case v.Stage == "error":
			button("_Cancel", dismiss)
		case v.Stage == "busy":
			spinner := gtk.NewSpinner()
			spinner.Start()
			box.Append(spinner)
			button("_Cancel", dismiss)
		default:
			entry = gtk.NewPasswordEntry()
			entry.SetShowPeekIcon(true)
			entry.SetHExpand(true)
			label := gtk.NewLabelWithMnemonic("_Response:")
			label.SetXAlign(0)
			label.SetMnemonicWidget(entry)
			box.Append(label)
			box.Append(entry)
			var remember *gtk.CheckButton
			if v.TTL > 0 {
				remember = gtk.NewCheckButtonWithLabel("Remember for " + v.TTL.String())
				remember.SetActive(true)
				box.Append(remember)
			}
			validation := gtk.NewLabel("")
			validation.SetWrap(true)
			validation.AddCSSClass("error")
			box.Append(validation)
			submit := func() {
				value := entry.Text()
				if err := askpass.Validate(value); err != nil {
					validation.SetText(err.Error())
					return
				}
				entry.SetText("")
				send(dialog.Action{Kind: "submit", Value: value, Remember: remember != nil && remember.Active()})
				win.SetSensitive(false)
			}
			entry.ConnectActivate(submit)
			if v.ChangeDevice {
				button("Change _device", func() { entry.SetText(""); send(dialog.Action{Kind: "change"}); win.SetSensitive(false) })
			}
			button("_Cancel", dismiss)
			win.SetDefaultWidget(button("_Submit", submit))
		}
		box.Append(buttons)
		win.SetSensitive(true)
		if v.PassiveTouch {
			win.Realize()
			if surface, ok := win.Surface().(*gdkx11.X11Surface); ok {
				// GTK deprecated its entire X11 backend, without a cross-backend
				// replacement for this EWMH no-focus-on-map hint.
				surface.SetUserTime(0) //nolint:staticcheck // Still required for non-activating X11 notifications.
			}
			win.SetVisible(true)
		} else {
			win.Present()
		}
		if entry != nil {
			entry.GrabFocus()
		}
		send(dialog.Action{Kind: "shown"})
	}
	a.ConnectActivate(func() {
		a.Hold()
		win = gtk.NewApplicationWindow(a)
		win.ConnectCloseRequest(func() bool { dismiss(); return true })
		keys := gtk.NewEventControllerKey()
		keys.SetPropagationPhase(gtk.PhaseCapture)
		keys.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
			if keyval == gdk.KEY_Escape {
				dismiss()
				return true
			}
			return false
		})
		win.AddController(keys)
		render(initial)
	})
	timer := glib.TimeoutAdd(40, func() bool {
		select {
		case v := <-views:
			render(v)
		default:
		}
		if current.Stage == "wait" && time.Now().After(nextRefresh) {
			nextRefresh = time.Now().Add(5 * time.Second)
			send(dialog.Action{Kind: "refresh"})
		}
		if ctx.Err() != nil {
			a.Quit()
		}
		return true
	})
	defer glib.SourceRemove(timer)
	code := a.Run([]string{"gtkaskpass-yubikey-ui"})
	if entry != nil {
		entry.SetText("")
	}
	if win != nil {
		win.Destroy()
	}
	if code != 0 {
		return errors.New("GTK worker failed")
	}
	return nil
}
