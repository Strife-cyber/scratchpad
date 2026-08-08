package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// This file implements clipboard read/write for the web engine
// (improvement-plan item 16). Reads go through navigator.clipboard (readText
// for text, read() for images) and writes through navigator.clipboard.writeText
// / ClipboardItem, with a document.execCommand('copy') textarea fallback for
// pages that lack the async clipboard API or its permission. Pasting uses the
// real CDP key events from item 15 (Ctrl+V / Cmd+V) so React-style controlled
// inputs receive the value.

// clipboardResult decodes the object returned by the JS clipboard helpers.
type clipboardResult struct {
	Text string `json:"text,omitempty"`
	Err  string `json:"err,omitempty"`
}

// clipboardImageResult decodes the object returned by the image read helper.
type clipboardImageResult struct {
	Mime   string `json:"mime,omitempty"`
	Base64 string `json:"base64,omitempty"`
	Err    string `json:"err,omitempty"`
}

// grantClipboard grants the clipboard-read/write permissions for the current
// origin so navigator.clipboard works, and brings the page to the front (some
// clipboard operations require a focused document). Failure is non-fatal: the
// execCommand fallbacks still apply.
func (e *ChromeEngine) grantClipboard(ctx context.Context) error {
	var origin string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`location.origin`, &origin)); err != nil {
		return err
	}
	if origin == "" || origin == "null" {
		origin = "https://example.invalid"
	}
	for _, name := range []string{
		string(browser.PermissionTypeClipboardReadWrite),
		string(browser.PermissionTypeClipboardSanitizedWrite),
	} {
		desc := &browser.PermissionDescriptor{Name: name}
		if err := browser.SetPermission(desc, browser.PermissionSettingGranted).
			WithOrigin(origin).
			Do(ctx); err != nil {
			return err
		}
	}
	_ = page.BringToFront().Do(ctx)
	return nil
}

// getClipboard reads the current clipboard contents (item 16). A MimeType
// starting with "image/" reads the first image item as base64; anything else
// (or empty) reads plain text.
func (e *ChromeEngine) getClipboard(ctx context.Context, mime string) (text, b64, gotMime string, err error) {
	if err := e.grantClipboard(ctx); err != nil {
		return "", "", "", err
	}
	if mime == "image" || strings.HasPrefix(mime, "image/") {
		return e.readClipboardImage(ctx)
	}
	text, err = e.readClipboardText(ctx)
	if err != nil {
		return "", "", "", err
	}
	return text, "", "text/plain", nil
}

// readClipboardText returns the clipboard text via navigator.clipboard.readText,
// falling back to document.execCommand('paste') into a hidden textarea.
func (e *ChromeEngine) readClipboardText(ctx context.Context) (string, error) {
	const js = `(async () => {
		try {
			return { text: await navigator.clipboard.readText() };
		} catch (err) {
			return { err: String(err && err.message || err) };
		}
	})()`
	var res clipboardResult
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &res)); err != nil {
		return "", fmt.Errorf("clipboard read: %w", err)
	}
	if res.Err == "" {
		return res.Text, nil
	}
	// Fallback: execCommand('paste') into a hidden textarea.
	return execCommandPaste(ctx)
}

// readClipboardImage reads the first image item from the clipboard and returns
// it base64-encoded (item 16).
func (e *ChromeEngine) readClipboardImage(ctx context.Context) (text, b64, mime string, err error) {
	const js = `(async () => {
		try {
			const items = await navigator.clipboard.read();
			for (const item of items) {
				for (const type of item.types) {
					if (type.startsWith('image/')) {
						const blob = await item.getType(type);
						const buf = new Uint8Array(await blob.arrayBuffer());
						let binary = '';
						for (let i = 0; i < buf.length; i += 0x8000) {
							binary += String.fromCharCode.apply(null, buf.subarray(i, i + 0x8000));
						}
						return { mime: type, base64: btoa(binary) };
					}
				}
			}
			return { err: 'clipboard: no image item on the clipboard' };
		} catch (err) {
			return { err: String(err && err.message || err) };
		}
	})()`
	var res clipboardImageResult
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &res)); err != nil {
		return "", "", "", fmt.Errorf("clipboard image read: %w", err)
	}
	if res.Err != "" {
		return "", "", "", errors.New(res.Err)
	}
	return "", res.Base64, res.Mime, nil
}

// setClipboard writes text (or an image from base64 when MimeType is an image
// MIME) to the clipboard (item 16).
func (e *ChromeEngine) setClipboard(ctx context.Context, text, mime string) error {
	if err := e.grantClipboard(ctx); err != nil {
		return err
	}
	if mime == "image" || strings.HasPrefix(mime, "image/") {
		data, err := decodeDataOrBase64(text)
		if err != nil {
			return fmt.Errorf("set_clipboard: invalid image base64: %w", err)
		}
		return e.writeClipboardImage(ctx, mime, data)
	}
	return e.setClipboardText(ctx, text)
}

// clipboardWriteTextJS builds the JS that writes text via
// navigator.clipboard.writeText, returning a {ok} or {err} object. Pure so the
// fallback behaviour is unit-testable.
func clipboardWriteTextJS(text string) string {
	return fmt.Sprintf(`(async () => {
		try {
			await navigator.clipboard.writeText(%s);
			return { ok: true };
		} catch (err) {
			return { err: String(err && err.message || err) };
		}
	})()`, jsStringLiteral(text))
}

// clipboardCopyJS builds the pre-Async-Clipboard-API fallback: copy text into a
// hidden textarea via document.execCommand('copy'). Returns whether it worked.
func clipboardCopyJS(text string) string {
	return fmt.Sprintf(`(() => {
		const ta = document.createElement('textarea');
		ta.value = %s;
		ta.setAttribute('readonly', '');
		ta.style.position = 'fixed';
		ta.style.left = '-9999px';
		document.body.appendChild(ta);
		ta.focus();
		ta.select();
		let ok = false;
		try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
		ta.remove();
		return ok;
	})()`, jsStringLiteral(text))
}

// clipboardPasteJS builds the pre-Async-Clipboard-API fallback: read clipboard
// text by pasting into a hidden textarea via document.execCommand('paste').
func clipboardPasteJS() string {
	return `(() => {
		const ta = document.createElement('textarea');
		ta.style.position = 'fixed';
		ta.style.left = '-9999px';
		document.body.appendChild(ta);
		ta.focus();
		let ok = false;
		try { ok = document.execCommand('paste'); } catch (e) { ok = false; }
		const val = ta.value;
		ta.remove();
		return { ok: ok, text: val };
	})()`
}

// setClipboardText writes text via navigator.clipboard.writeText, falling back
// to the document.execCommand('copy') textarea trick for pages without the
// async clipboard API or permission.
func (e *ChromeEngine) setClipboardText(ctx context.Context, text string) error {
	var res clipboardResult
	if err := chromedp.Run(ctx, chromedp.Evaluate(clipboardWriteTextJS(text), &res)); err != nil {
		return fmt.Errorf("clipboard write: %w", err)
	}
	if res.Err == "" {
		return nil
	}
	return execCommandCopy(ctx, text)
}

// writeClipboardImage writes an image (from bytes) to the clipboard as a
// ClipboardItem (item 16).
func (e *ChromeEngine) writeClipboardImage(ctx context.Context, mime string, data []byte) error {
	b64 := base64.StdEncoding.EncodeToString(data)
	js := fmt.Sprintf(`(async () => {
		try {
			const bytes = Uint8Array.from(atob(%s), c => c.charCodeAt(0));
			const blob = new Blob([bytes], {type: %s});
			const item = new ClipboardItem({[%s]: blob});
			await navigator.clipboard.write([item]);
			return { ok: true };
		} catch (err) {
			return { err: String(err && err.message || err) };
		}
	})()`, jsStringLiteral(b64), jsStringLiteral(mime), jsStringLiteral(mime))
	var res clipboardResult
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &res)); err != nil {
		return fmt.Errorf("clipboard image write: %w", err)
	}
	if res.Err != "" {
		return fmt.Errorf("clipboard image write: %s", res.Err)
	}
	return nil
}

// pasteClipboard dispatches a real paste shortcut (Cmd+V / Ctrl+V) at the
// current focus via Input.dispatchKeyEvent (item 15), so React-style inputs
// receive the pasted value.
func (e *ChromeEngine) pasteClipboard(ctx context.Context) error {
	mods := primaryModifier()
	down := input.DispatchKeyEvent(input.KeyDown).
		WithKey("v").WithCode("KeyV").
		WithWindowsVirtualKeyCode(86).WithNativeVirtualKeyCode(86).
		WithModifiers(mods)
	if err := down.Do(ctx); err != nil {
		return err
	}
	up := input.DispatchKeyEvent(input.KeyUp).
		WithKey("v").WithCode("KeyV").
		WithWindowsVirtualKeyCode(86).WithNativeVirtualKeyCode(86).
		WithModifiers(mods)
	return up.Do(ctx)
}

// execCommandCopy copies text into the clipboard via a hidden textarea and
// document.execCommand('copy') — the pre-Async-Clipboard-API fallback.
func execCommandCopy(ctx context.Context, text string) error {
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(clipboardCopyJS(text), &ok)); err != nil {
		return fmt.Errorf("clipboard write (execCommand copy): %w", err)
	}
	if !ok {
		return fmt.Errorf("clipboard write failed: navigator.clipboard.writeText and execCommand('copy') both failed")
	}
	return nil
}

// execCommandPaste reads clipboard text via a hidden textarea and
// document.execCommand('paste') — the pre-Async-Clipboard-API fallback.
func execCommandPaste(ctx context.Context) (string, error) {
	var res clipboardResult
	if err := chromedp.Run(ctx, chromedp.Evaluate(clipboardPasteJS(), &res)); err != nil {
		return "", fmt.Errorf("clipboard read (execCommand paste): %w", err)
	}
	if res.Text == "" {
		return "", fmt.Errorf("clipboard read failed: navigator.clipboard.readText and execCommand('paste') both failed")
	}
	return res.Text, nil
}
