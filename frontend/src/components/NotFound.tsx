import { Button } from '@gravity-ui/uikit'
import { Link } from '@tanstack/react-router'

export function NotFound({ children }: { children?: any }) {
  return (
    <div className="hs-col" style={{ padding: 'var(--g-spacing-4)' }}>
      <div>{children || <p>Такой страницы не существует.</p>}</div>
      <div className="hs-row">
        <Button onClick={() => window.history.back()}>Назад</Button>
        <Link to="/" className="hs-nav-link">
          <Button view="outlined-action">На главную</Button>
        </Link>
      </div>
    </div>
  )
}
