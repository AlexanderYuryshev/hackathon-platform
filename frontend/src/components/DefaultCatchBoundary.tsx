import { Button } from '@gravity-ui/uikit'
import {
  ErrorComponent,
  Link,
  useLocation,
  useRouter,
} from '@tanstack/react-router'
import type { ErrorComponentProps } from '@tanstack/react-router'

export function DefaultCatchBoundary({ error }: ErrorComponentProps) {
  const router = useRouter()
  const isRoot = useLocation({
    select: (location) => location.pathname === '/',
  })

  console.error(error)

  return (
    <div
      className="hs-col hs-center"
      style={{
        padding: 'var(--g-spacing-6)',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <ErrorComponent error={error} />
      <div className="hs-row">
        <Button onClick={() => router.invalidate()}>Повторить</Button>
        {isRoot ? (
          <Link to="/" className="hs-nav-link">
            <Button view="outlined">На главную</Button>
          </Link>
        ) : (
          <Link
            to="/"
            className="hs-nav-link"
            onClick={(e) => {
              e.preventDefault()
              window.history.back()
            }}
          >
            <Button view="outlined">Назад</Button>
          </Link>
        )}
      </div>
    </div>
  )
}
