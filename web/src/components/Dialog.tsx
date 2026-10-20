import { signal } from '@preact/signals'
import { useEffect, useRef, useState } from 'preact/hooks'
import { LogoMark } from './Icons'

// FairPlay's own dialogs, in place of the browser's confirm() and prompt(),
// which open at the top of the window in the browser's own style. One dialog
// at a time; Escape or a click outside cancels, Enter confirms.

interface Request {
  title: string
  message?: string
  confirm: string
  cancel: string
  danger: boolean
  input?: { value: string; placeholder?: string; maxLength?: number; label?: string }
  resolve: (value: string | null) => void
}

const current = signal<Request | null>(null)

interface AskOptions {
  title: string
  message?: string
  confirm?: string
  cancel?: string
  /** Destructive action: the confirm button is red. */
  danger?: boolean
}

/** Ask a yes/no question. Resolves true on confirm. */
export function ask(o: AskOptions): Promise<boolean> {
  return new Promise((resolve) => {
    current.value?.resolve(null)
    current.value = {
      title: o.title,
      message: o.message,
      confirm: o.confirm || 'OK',
      cancel: o.cancel || 'Cancel',
      danger: !!o.danger,
      resolve: (v) => resolve(v !== null),
    }
  })
}

/** Ask for a line of text. Resolves the trimmed text, or null when cancelled or empty. */
export function askText(o: AskOptions & { value?: string; placeholder?: string; maxLength?: number; label?: string }): Promise<string | null> {
  return new Promise((resolve) => {
    current.value?.resolve(null)
    current.value = {
      title: o.title,
      message: o.message,
      confirm: o.confirm || 'Save',
      cancel: o.cancel || 'Cancel',
      danger: !!o.danger,
      input: { value: o.value || '', placeholder: o.placeholder, maxLength: o.maxLength, label: o.label },
      resolve: (v) => resolve(v === null ? null : v.trim() || null),
    }
  })
}

/** Mounted once at the app root. */
export function DialogHost() {
  const req = current.value
  const [text, setText] = useState('')
  const confirmRef = useRef<HTMLButtonElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!req) return
    setText(req.input?.value || '')
    // Focus the field (or the confirm button) once the dialog is on screen.
    requestAnimationFrame(() => {
      if (req.input) {
        inputRef.current?.focus()
        inputRef.current?.select()
      } else confirmRef.current?.focus()
    })
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        close(null)
      }
    }
    addEventListener('keydown', onKey, true)
    return () => removeEventListener('keydown', onKey, true)
  }, [req])

  if (!req) return null

  function close(value: string | null) {
    const r = current.value
    current.value = null
    r?.resolve(value)
  }
  const submit = (e: Event) => {
    e.preventDefault()
    close(req.input ? text : '')
  }

  return (
    <div class="modal-back dialog-back" onClick={() => close(null)}>
      <form
        class={`dialog ${req.danger ? 'danger' : ''}`}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
        onClick={(e) => e.stopPropagation()}
        onSubmit={submit}
        onKeyDown={(e) => e.stopPropagation()}
      >
        <div class="dialog-head">
          <LogoMark size={26} />
          <h2 id="dialog-title" class="dialog-title">
            {req.title}
          </h2>
        </div>
        {req.message && <p class="dialog-msg">{req.message}</p>}
        {req.input && (
          <label class="col tight">
            {req.input.label && <span class="label dim">{req.input.label}</span>}
            <input
              ref={inputRef}
              class="input"
              value={text}
              placeholder={req.input.placeholder}
              maxLength={req.input.maxLength}
              onInput={(e) => setText((e.target as HTMLInputElement).value)}
            />
          </label>
        )}
        <div class="dialog-actions">
          <button type="button" class="btn btn-quiet" onClick={() => close(null)}>
            {req.cancel}
          </button>
          <button ref={confirmRef} type="submit" class={`btn ${req.danger ? 'btn-danger-solid' : 'btn-primary'}`} disabled={!!req.input && !text.trim()}>
            {req.confirm}
          </button>
        </div>
      </form>
    </div>
  )
}
