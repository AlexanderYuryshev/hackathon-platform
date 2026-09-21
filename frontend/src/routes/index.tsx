import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Button, Card, Label, Text } from '@gravity-ui/uikit'
import { api } from '~/lib/api'
import { hackathonStatusRu, roleRu, ru } from '~/lib/labels'
import { useMyRoles } from '~/lib/roles'
import type { Hackathon } from '~/lib/types'

export const Route = createFileRoute('/')({
  component: HomePage,
})

const statusLabels: Record<string, { theme: 'utility' | 'success' | 'warning' | 'info' }> = {
  draft: { theme: 'utility' },
  running: { theme: 'success' },
  judging: { theme: 'warning' },
  finalized: { theme: 'info' },
}

function HomePage() {
  const hackathons = useQuery({
    queryKey: ['hackathons'],
    queryFn: () => api.get<{ hackathons: Hackathon[] }>('/hackathons'),
  })
  // Показываем только те разделы, куда пользователь реально может попасть:
  // судья не видит панель организатора, участник — судейскую и т.д.
  const { isAuthenticated, roles } = useMyRoles()
  const list = hackathons.data?.hackathons ?? []

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-6)', paddingTop: 'var(--g-spacing-6)' }}>
      <div className="hs-col hs-center" style={{ alignItems: 'center', gap: 'var(--g-spacing-3)' }}>
        <Text variant="display-2" as="h1">
          Скоринг хакатонов в реальном времени
        </Text>
        <Text variant="body-2" color="secondary" style={{ maxWidth: 560 }}>
          Прозрачный скоринг команд: автоматические метрики, судейские критерии,
          устойчивая к выбросам агрегация и онлайн-лидерборд с полной расшифровкой оценки.
        </Text>
        {isAuthenticated && (
          <Link to="/hackathons/new" className="hs-nav-link">
            <Button view="action" size="l">
              Создать хакатон
            </Button>
          </Link>
        )}
      </div>

      {hackathons.isLoading && (
        <Text variant="body-2" color="secondary" className="hs-center">
          Загрузка…
        </Text>
      )}
      {hackathons.isError && (
        <Text variant="body-2" color="secondary" className="hs-center">
          Не удалось загрузить хакатоны (API недоступен).
        </Text>
      )}

      {list.length > 0 && (
        <div className="hs-grid hs-grid-cards">
          {list.map((h) => {
            const role = roles[h.id]
            // Судьям лидерборд закрыт слепотой до финализации — зато у них
            // есть своя панель, и с главной туда теперь можно попасть.
            const canViewBoard = role !== 'judge' || h.status === 'finalized'
            return (
              <Card key={h.id} type="container" view="outlined">
                <div className="hs-card-content">
                  <div className="hs-justify-between">
                    <Text variant="subheader-2">{h.name}</Text>
                    <Label theme={statusLabels[h.status]?.theme ?? 'utility'} size="s">
                      {ru(hackathonStatusRu, h.status)}
                    </Label>
                  </div>
                  {role && (
                    <Label theme={role === 'organizer' ? 'info' : role === 'judge' ? 'warning' : 'success'} size="s">
                      вы: {ru(roleRu, role)}
                    </Label>
                  )}
                  <Text variant="code-inline-2" color="secondary">
                    /h/{h.slug}
                  </Text>
                  {h.description && (
                    <Text variant="body-2" color="secondary" ellipsisLines={2}>
                      {h.description}
                    </Text>
                  )}
                  <div className="hs-row" style={{ gap: 'var(--g-spacing-4)', marginTop: 'auto' }}>
                    {canViewBoard && (
                      <Link to="/h/$slug" params={{ slug: h.slug }} className="hs-nav-link">
                        <Text variant="body-2" color="link">
                          Лидерборд
                        </Text>
                      </Link>
                    )}
                    {role === 'judge' && (
                      <Link to="/judge/$slug" params={{ slug: h.slug }} className="hs-nav-link">
                        <Button view="action" size="m">
                          Войти в судейство
                        </Button>
                      </Link>
                    )}
                    {role === 'organizer' && (
                      <Link to="/app/hackathons/$id" params={{ id: h.id }} className="hs-nav-link">
                        <Text variant="body-2" color="secondary">
                          Панель организатора
                        </Text>
                      </Link>
                    )}
                    {role !== 'judge' && role !== 'organizer' && (
                      <Link to="/team/$slug" params={{ slug: h.slug }} className="hs-nav-link">
                        <Text variant="body-2" color="link">
                          Кабинет команды
                        </Text>
                      </Link>
                    )}
                  </div>
                </div>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
