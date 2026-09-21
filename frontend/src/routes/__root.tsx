/// <reference types="vite/client" />
import {
  HeadContent,
  Link,
  Outlet,
  Scripts,
  createRootRouteWithContext,
  useRouter,
} from '@tanstack/react-router'
import * as React from 'react'
import { useCallback, useEffect, useState } from 'react'
import type { QueryClient } from '@tanstack/react-query'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Icon, Text, ThemeProvider, ToasterComponent, ToasterProvider } from '@gravity-ui/uikit'
import { Moon, Sun } from '@gravity-ui/icons'
import { toaster } from '@gravity-ui/uikit/toaster-singleton'
import { logout, me, setCsrfToken } from '~/lib/api'
import { DefaultCatchBoundary } from '~/components/DefaultCatchBoundary'
import { NotFound } from '~/components/NotFound'
import appCss from '~/styles/app.css?url'
import uikitFonts from '@gravity-ui/uikit/styles/fonts.css?url'
import uikitStyles from '@gravity-ui/uikit/styles/styles.css?url'

export const Route = createRootRouteWithContext<{
  queryClient: QueryClient
}>()({
  head: () => ({
    meta: [
      { charSet: 'utf-8' },
      { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      { title: 'Скоринг хакатонов' },
    ],
    links: [
      { rel: 'stylesheet', href: uikitFonts },
      { rel: 'stylesheet', href: uikitStyles },
      { rel: 'stylesheet', href: appCss },
    ],
  }),
  errorComponent: (props) => (
    <ThemedRoot>
      <DefaultCatchBoundary {...props} />
    </ThemedRoot>
  ),
  notFoundComponent: () => <NotFound />,
  component: RootComponent,
})

type ThemeMode = 'light' | 'dark'

const THEME_STORAGE_KEY = 'hs-theme'

// Начальная тема: сохранённый выбор пользователя, иначе — системная
// (старое значение 'system' тоже трактуем как «выбора не было»).
function resolveInitialTheme(): ThemeMode {
  if (typeof window === 'undefined' || typeof localStorage === 'undefined') {
    return 'light'
  }
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    if (v === 'light' || v === 'dark') {
      return v
    }
  } catch {
    // Приватный режим и т.п. — упадём на системную ниже.
  }
  if (
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches
  ) {
    return 'dark'
  }
  return 'light'
}

function RootComponent() {
  return (
    <ThemedRoot>
      <Outlet />
    </ThemedRoot>
  )
}

// Владеет темой: дальше — только явный тоггл пользователя, хранится
// в localStorage. Первый рендер (и на сервере, и на клиенте) обязан быть
// 'light': иначе гидратация увидит расхождение с SSR-разметкой, React
// оставит серверный DOM как есть — и первое нажатие визуально ничего
// не поменяет (тема сменится, а иконка останется прежней). Реальную тему
// (сохранённый выбор или системную) подтягиваем эффектом после монтирования.
// ThemeProvider без scoped не рендерит DOM (тема вешается
// на body в layout-эффекте), поэтому этот ре-рендер безопасен.
function ThemedRoot({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<ThemeMode>('light')
  useEffect(() => {
    setTheme(resolveInitialTheme())
  }, [])
  const toggleTheme = useCallback(() => {
    setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'))
  }, [])
  useEffect(() => {
    try {
      localStorage.setItem(THEME_STORAGE_KEY, theme)
    } catch {
      // Приватный режим и т.п. — тема просто не сохранится.
    }
  }, [theme])
  return (
    <RootDocument theme={theme} onToggleTheme={toggleTheme}>
      {children}
    </RootDocument>
  )
}

function RootDocument({
  children,
  theme,
  onToggleTheme,
}: {
  children: React.ReactNode
  theme: ThemeMode
  onToggleTheme: () => void
}) {
  return (
    <html lang="ru">
      <head>
        <HeadContent />
      </head>
      <body className="g-root g-root_theme_light">
        <ThemeProvider theme={theme}>
          <ToasterProvider toaster={toaster}>
            <header className="hs-header">
              <div
                className="hs-page hs-row"
                style={{ gap: 'var(--g-spacing-5)', justifyContent: 'space-between' }}
              >
                <Link to="/" className="hs-nav-link">
                  <Text variant="header-2">⚡ Скоринг хакатонов</Text>
                </Link>
                <nav className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
                  <Link to="/" className="hs-nav-link">
                    <Text variant="body-2" color="link">
                      Хакатоны
                    </Text>
                  </Link>
                  <HeaderCreateLink />
                  <Button
                    view="flat"
                    size="s"
                    onClick={onToggleTheme}
                    title={theme === 'dark' ? 'Переключить на светлую тему' : 'Переключить на тёмную тему'}
                    aria-label={theme === 'dark' ? 'Переключить на светлую тему' : 'Переключить на тёмную тему'}
                  >
                    <Icon data={theme === 'dark' ? Sun : Moon} />
                  </Button>
                  <HeaderAuth />
                </nav>
              </div>
            </header>
            <main className="hs-page">{children}</main>
            <ToasterComponent />
            <Scripts />
          </ToasterProvider>
        </ThemeProvider>
      </body>
    </html>
  )
}

function HeaderCreateLink() {
  // Создание хакатона требует авторизации — анонимами ссылку не показываем,
  // чтобы не вести на страницу с требованием входа.
  const user = useQuery({ queryKey: ['me'], queryFn: () => me(), retry: false, staleTime: 60_000 })
  if (!user.data?.user) return null
  return (
    <Link to="/hackathons/new" className="hs-nav-link">
      <Text variant="body-2" color="link">
        + Создать хакатон
      </Text>
    </Link>
  )
}

function HeaderAuth() {
  const queryClient = useQueryClient()
  const router = useRouter()
  const user = useQuery({
    queryKey: ['me'],
    queryFn: () => me(),
    retry: false,
    staleTime: 60_000,
  })

  if (user.data?.user) {
    return (
      <div className="hs-row" style={{ gap: 'var(--g-spacing-3)' }}>
        <Text variant="body-2" color="secondary">
          {user.data.user.name || user.data.user.email}
        </Text>
        <Button
          view="flat"
          size="s"
          onClick={async () => {
            try {
              await logout()
            } catch {
              // Сессия может быть уже мертва (истёкший CSRF-токен и т.п.) —
              // локальное состояние чистим в любом случае.
            } finally {
              setCsrfToken('')
              // resetQueries, а не clear(): clear() лишь удаляет записи кэша —
              // смонтированный observer ['me'] рефетч не запускает и дальше
              // показывает протухшее состояние, а провал фонового рефетча
              // в TanStack Query v5 сохраняет старые данные. reset синхронно
              // сбрасывает auth-кэш (шапка сразу показывает «Войти»).
              queryClient.resetQueries({ queryKey: ['me'] })
              // Остальные активные запросы освежаем уже анонимно.
              await queryClient.invalidateQueries()
              await router.invalidate()
            }
          }}
        >
          Выйти
        </Button>
      </div>
    )
  }
  return (
    <Link to="/login" className="hs-nav-link">
      <Text variant="body-2" color="link">
        Войти
      </Text>
    </Link>
  )
}
