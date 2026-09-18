// SPDX-License-Identifier: Apache-2.0
package gtkui

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"gtkaskpass-yubikey/internal/app"
	"gtkaskpass-yubikey/internal/askpass"
	"gtkaskpass-yubikey/internal/trace"
)

type UI struct{}

// Show must run on the initial locked OS thread. Every callback and widget
// access stays on GTK's main context, including cancellation processing.
func (UI) Show(ctx context.Context, req askpass.Request, ttl time.Duration, log *trace.Logger) (app.Result, error) {
	var result app.Result
	if ctx.Err() != nil {
		return result, nil
	}
	if !gtk.InitCheck() {
		return result, errors.New("cannot connect to a graphical display")
	}
	a := gtk.NewApplication("io.github.gtkaskpass_yubikey", gio.ApplicationNonUnique)
	var win *gtk.ApplicationWindow
	var entry *gtk.PasswordEntry
	finished := false
	finish := func(r app.Result) {
		if finished {
			return
		}
		finished = true
		result = r
		if entry != nil {
			entry.SetText("")
		}
		if win != nil {
			win.Destroy()
		}
		a.Quit()
	}
	timer := glib.TimeoutAdd(50, func() bool {
		if ctx.Err() != nil {
			log.Event("cancelled-context")
			finish(app.Result{})
		}
		return true
	})
	defer glib.SourceRemove(timer)
	a.ConnectActivate(func() {
		if ctx.Err() != nil {
			finish(app.Result{})
			return
		}
		a.Hold() // notifications remain alive after their window is dismissed
		win = gtk.NewApplicationWindow(a)
		win.SetTitle(req.Title)
		win.SetDefaultSize(480, -1)
		box := gtk.NewBox(gtk.OrientationVertical, 12)
		box.SetMarginTop(20)
		box.SetMarginBottom(20)
		box.SetMarginStart(20)
		box.SetMarginEnd(20)
		win.SetChild(box)
		prompt := gtk.NewLabel(req.Prompt)
		prompt.SetWrap(true)
		prompt.SetXAlign(0)
		prompt.SetSelectable(true)
		prompt.SetMaxWidthChars(64)
		scroll := gtk.NewScrolledWindow()
		scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
		scroll.SetPropagateNaturalHeight(true)
		scroll.SetMaxContentHeight(300)
		scroll.SetChild(prompt)
		box.Append(scroll)
		buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
		buttons.SetHAlign(gtk.AlignEnd)
		dismiss := func() {
			if req.Mode == askpass.Notify {
				log.Event("dismiss")
				win.SetVisible(false)
			} else {
				log.Event("cancel")
				finish(app.Result{})
			}
		}
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
		switch req.Mode {
		case askpass.Notify:
			spinner := gtk.NewSpinner()
			spinner.Start()
			box.Append(spinner)
			label := gtk.NewLabel("Waiting for SSH. Dismissing this window does not cancel signing.")
			label.SetWrap(true)
			label.SetMaxWidthChars(60)
			box.Append(label)
			b := gtk.NewButtonWithMnemonic("_Dismiss")
			b.ConnectClicked(dismiss)
			buttons.Append(b)
		case askpass.Confirm:
			deny := gtk.NewButtonWithMnemonic("_Deny")
			deny.ConnectClicked(dismiss)
			buttons.Append(deny)
			allow := gtk.NewButtonWithMnemonic("_Allow")
			allow.ConnectClicked(func() { log.Event("allow"); finish(app.Result{Accepted: true}) })
			buttons.Append(allow)
			win.SetDefaultWidget(deny)
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
			if ttl > 0 {
				remember = gtk.NewCheckButtonWithLabel("Remember for " + ttl.String())
				remember.SetActive(true)
				box.Append(remember)
			}
			validation := gtk.NewLabel("")
			validation.SetWrap(true)
			validation.SetXAlign(0)
			validation.AddCSSClass("error")
			box.Append(validation)
			submit := func() {
				value := entry.Text()
				if err := askpass.Validate(value); err != nil {
					validation.SetText(err.Error())
					return
				}
				log.Event("submit")
				finish(app.Result{Value: value, Accepted: true, Remember: remember != nil && remember.Active()})
			}
			entry.ConnectActivate(submit)
			cancel := gtk.NewButtonWithMnemonic("_Cancel")
			cancel.ConnectClicked(dismiss)
			buttons.Append(cancel)
			ok := gtk.NewButtonWithMnemonic("_Submit")
			ok.AddCSSClass("suggested-action")
			ok.ConnectClicked(submit)
			buttons.Append(ok)
			win.SetDefaultWidget(ok)
		}
		box.Append(buttons)
		win.Present()
		if entry != nil {
			entry.GrabFocus()
		}
		log.Event("window-shown", "mode", req.Mode)
	})
	status := a.Run([]string{"gtkaskpass-yubikey"})
	if status != 0 {
		return result, errors.New("GTK application failed")
	}
	return result, nil
}
