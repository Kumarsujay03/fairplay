import { render } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import './styles.css'
import './player.css'
import * as S from './store'
import { Join } from './pages/Join'
import { Room } from './pages/Room'
import { Host } from './pages/Host'
import { Landing } from './pages/Landing'
import { DialogHost } from './components/Dialog'

function App() {
  const [path, setPath] = useState(location.pathname)
  useEffect(() => {
    const on = () => setPath(location.pathname)
    addEventListener('popstate', on)
    return () => removeEventListener('popstate', on)
  }, [])

  let page
  const j = path.match(/^\/j\/([A-Za-z0-9_-]{6,32})$/)
  if (j) page = <Join id={j[1]} />
  else if (path === '/room') page = <Room />
  else if (path === '/host') page = <Host />
  else page = <Landing />

  const t = S.toast.value
  return (
    <>
      {page}
      <DialogHost />
      {t && (
        <div class={`toast ${t.kind === 'error' ? 'error' : ''}`} role="status">
          {t.text}
        </div>
      )}
    </>
  )
}

render(<App />, document.getElementById('app')!)
