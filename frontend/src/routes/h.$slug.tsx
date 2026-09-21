import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import type { TableColumnConfig } from '@gravity-ui/uikit'
import { Drawer, Label, Progress, Select, Table, Text, TextInput } from '@gravity-ui/uikit'
import { api, ssrRequestHeaders, ApiError } from '~/lib/api'
import { defaultTrackKey, useTracks } from '~/lib/tracks'
import { roleOf, useMyRoles } from '~/lib/roles'
import { RoleBadge, RoleHint } from '~/components/RoleBadge'
import { hackathonStatusRu, metricStatusRu, ru, scoreStatusRu } from '~/lib/labels'
import type { Hackathon, LeaderRow, LeaderboardResponse, TeamDetailResponse, Track } from '~/lib/types'
import { Sparkline } from '~/components/charts'
import { useLeaderboardEvents } from '~/lib/sse'

export const Route = createFileRoute('/h/$slug')({
  loader: async ({ params, context: { queryClient } }) => {
    const ssrHeaders = await ssrRequestHeaders()
    const hackathon = await queryClient.ensureQueryData({
      queryKey: ['hackathon', params.slug],
      queryFn: () => api.get<Hackathon>(`/hackathons/${params.slug}`, ssrHeaders),
    })
    const { tracks } = await queryClient.ensureQueryData({
      queryKey: ['tracks', hackathon.id],
      queryFn: () => api.get<{ tracks: Track[] }>(`/hackathons/${hackathon.id}/tracks`, ssrHeaders),
    })
    const trackKey = defaultTrackKey(tracks)
    if (trackKey) {
      await queryClient.ensureQueryData({
        queryKey: ['leaderboard', hackathon.id, trackKey, '', '', ''],
        queryFn: () =>
          api.get<LeaderboardResponse>(`/hackathons/${hackathon.id}/leaderboard?track=${trackKey}`, ssrHeaders),
      })
    }
  },
  component: LeaderboardPage,
  errorComponent: ({ error }) => {
    if (error instanceof ApiError && error.code === 'judge_blind') {
      return (
        <Text variant="body-1" color="secondary" className="hs-center" style={{ padding: 'var(--g-spacing-6)', display: 'block' }}>
          Лидерборд скрыт на время судейства. Он станет доступен после финализации.
        </Text>
      )
    }
    return (
      <Text variant="body-1" color="danger" className="hs-center" style={{ padding: 'var(--g-spacing-6)', display: 'block' }}>
        {error instanceof Error ? error.message : 'Не удалось загрузить лидерборд'}
      </Text>
    )
  },
})

const hackathonStatusTheme: Record<string, 'utility' | 'success' | 'warning' | 'info'> = {
  draft: 'utility',
  running: 'success',
  judging: 'warning',
  finalized: 'info',
}

function useHackathon(slug: string) {
  return useQuery({
    queryKey: ['hackathon', slug],
    queryFn: () => api.get<Hackathon>(`/hackathons/${slug}`),
  })
}

// Formatted on the client only: server and browser locales differ, which would
// cause a hydration mismatch for the same timestamp.
function LocalTime({ iso, kind = 'datetime' }: { iso: string; kind?: 'time' | 'datetime' }) {
  const [text, setText] = useState('')
  useEffect(() => {
    const d = new Date(iso)
    setText(kind === 'time' ? d.toLocaleTimeString() : d.toLocaleString())
  }, [iso, kind])
  return <>{text}</>
}

function useLeaderboard(id: string | undefined, trackKey: string, status: string, q: string) {
  return useQuery({
    queryKey: ['leaderboard', id, trackKey, status, q],
    queryFn: () => {
      const params = new URLSearchParams()
      if (trackKey) params.set('track', trackKey)
      if (status) params.set('status', status)
      if (q) params.set('q', q)
      const qs = params.toString()
      return api.get<LeaderboardResponse>(`/hackathons/${id}/leaderboard${qs ? `?${qs}` : ''}`)
    },
    enabled: !!id && !!trackKey,
  })
}

const columns: TableColumnConfig<LeaderRow>[] = [
  { id: 'rank', name: '№', width: 48, align: 'center' },
  { id: 'team_name', name: 'Команда' },
  {
    id: 'score',
    name: 'Балл',
    align: 'end',
    template: (row) => <Text variant="body-2">{row.score.toFixed(2)}</Text>,
  },
  {
    id: 'score_delta',
    name: 'Δ балла',
    align: 'end',
    template: (row) =>
      row.score_delta == null ? (
        <Text variant="body-2" color="hint">
          —
        </Text>
      ) : (
        <Text
          variant="body-2"
          className={row.score_delta >= 0 ? 'hs-score-delta-pos' : 'hs-score-delta-neg'}
        >
          {row.score_delta >= 0 ? '+' : ''}
          {row.score_delta.toFixed(2)}
        </Text>
      ),
  },
  {
    id: 'rank_delta',
    name: 'Δ места',
    align: 'center',
    template: (row) =>
      row.rank_delta == null || row.rank_delta === 0 ? (
        <Text variant="body-2" color="hint">
          —
        </Text>
      ) : row.rank_delta > 0 ? (
        <Text variant="body-2" className="hs-rank-up">
          ▲{row.rank_delta}
        </Text>
      ) : (
        <Text variant="body-2" className="hs-rank-down">
          ▼{-row.rank_delta}
        </Text>
      ),
  },
  {
    id: 'completeness',
    name: 'Полнота',
    width: 180,
    template: (row) => (
      <div className="hs-row" style={{ gap: 'var(--g-spacing-2)' }}>
        <div className="hs-grow">
          <Progress value={row.completeness} size="s" />
        </div>
        <Text variant="caption-2" color="secondary">
          {row.completeness}%
        </Text>
      </div>
    ),
  },
  {
    id: 'status',
    name: 'Статус',
    template: (row) =>
      row.status === 'provisional' ? (
        <Label theme="warning" size="s">
          {scoreStatusRu.provisional}
        </Label>
      ) : (
        <Label theme="success" size="s">
          {scoreStatusRu.complete}
        </Label>
      ),
  },
  {
    id: 'trend',
    name: 'Динамика',
    template: (row) => <Sparkline values={row.sparkline} />,
  },
]

function LeaderboardPage() {
  const { slug } = Route.useParams()
  const hackathon = useHackathon(slug)
  const [pickedTrack, setPickedTrack] = useState<string | null>(null)
  const [status, setStatus] = useState('')
  const [qInput, setQInput] = useState('')
  const [q, setQ] = useState('')
  const [selected, setSelected] = useState<string | null>(null)

  useEffect(() => {
    const t = setTimeout(() => setQ(qInput.trim()), 300)
    return () => clearTimeout(t)
  }, [qInput])

  const id = hackathon.data?.id
  const tracksQuery = useTracks(id)
  const tracks = tracksQuery.data?.tracks ?? []
  // Навигация только туда, куда ролью разрешён доступ: у каждой роли своё
  // рабочее место (участник — кабинет команды, судья — панель судьи,
  // организатор — панель организатора). Организатор и судья в кабинет команды
  // не ходят: у первого команды нет по определению, у второго — конфликт интересов.
  const { roles } = useMyRoles()
  const role = id ? roleOf(roles, id) : undefined
  const showTeamCabinet = role === undefined || role === 'team_member'
  // The switcher is hidden for single-track hackathons: no pointless UI.
  const trackKey = pickedTrack ?? defaultTrackKey(tracks)
  const board = useLeaderboard(id, trackKey, status, q)
  const { connected } = useLeaderboardEvents(id, trackKey, true)

  if (hackathon.isLoading) {
    return (
      <Text variant="body-1" color="secondary" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Загрузка…
      </Text>
    )
  }
  if (hackathon.isError || !hackathon.data) {
    return (
      <Text variant="body-1" color="danger" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Хакатон не найден
      </Text>
    )
  }

  const h = hackathon.data
  const rows = board.data?.leaderboard ?? []

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)' }}>
      <div className="hs-justify-between">
        <div className="hs-col" style={{ gap: 'var(--g-spacing-2)' }}>
          <Text variant="header-1" as="h1">
            {h.name}
          </Text>
          <div className="hs-row" style={{ gap: 'var(--g-spacing-2)' }}>
            <Label theme={hackathonStatusTheme[h.status] ?? 'utility'} size="m">
              {ru(hackathonStatusRu, h.status)}
            </Label>
            <RoleBadge role={role} />
            {h.ends_at && (
              <Label theme="info" size="m">
                окончание: <LocalTime iso={h.ends_at} />
              </Label>
            )}
          </div>
          <RoleHint role={role} />
          {board.data?.meta?.generated_at && (
            <Text variant="body-2" color="secondary">
              Обновлено <LocalTime iso={board.data.meta.generated_at} kind="time" />
            </Text>
          )}
        </div>
        <div className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
          {showTeamCabinet && (
            <Link to="/team/$slug" params={{ slug }} className="hs-nav-link">
              <Text variant="body-2" color="link">
                Кабинет команды
              </Text>
            </Link>
          )}
          {role === 'judge' && (
            <Link to="/judge/$slug" params={{ slug }} className="hs-nav-link">
              <Text variant="body-2" color="link">
                Панель судьи
              </Text>
            </Link>
          )}
          {role === 'organizer' && (
            <Link to="/app/hackathons/$id" params={{ id: h.id }} className="hs-nav-link">
              <Text variant="body-2" color="link">
                Панель организатора
              </Text>
            </Link>
          )}
        </div>
      </div>

      <div className="hs-row hs-row-end">
        <TextInput
          value={qInput}
          onChange={(e) => setQInput(e.target.value)}
          placeholder="Поиск команды…"
          hasClear
          style={{ minWidth: 200 }}
        />
        {tracks.length > 1 && (
          <Select
            label="Трек"
            value={trackKey ? [trackKey] : []}
            onUpdate={(v) => setPickedTrack(Array.isArray(v) ? (v[0] ?? null) : (v ?? null))}
            options={tracks.map((t) => ({ value: t.key, content: t.name }))}
            className="hs-w-select"
          />
        )}
        <Select
          placeholder="Все статусы"
          value={status ? [status] : []}
          onUpdate={(v) => setStatus(Array.isArray(v) ? (v[0] ?? '') : (v ?? ''))}
          hasClear
          options={[
            { value: 'complete', content: 'Полный' },
            { value: 'provisional', content: 'Предварительный' },
          ]}
          className="hs-w-select"
        />
        <Text
          variant="caption-1"
          color={connected ? 'positive' : 'warning'}
          className="hs-grow"
          style={{ textAlign: 'right' }}
        >
          {board.isFetching ? 'обновление…' : connected ? 'онлайн' : 'переподключение…'}
        </Text>
      </div>

      {board.isError ? (
        <Text variant="body-1" color="danger" className="hs-center" style={{ padding: 'var(--g-spacing-4)' }}>
          Не удалось загрузить лидерборд: {(board.error as Error)?.message ?? 'ошибка сети'}
        </Text>
      ) : (
        <Table
          data={rows}
          columns={columns}
          getRowId={(row) => row.team_id}
          onRowClick={(row) => setSelected(row.team_id)}
          emptyMessage="Нет команд по заданным фильтрам"
        />
      )}

      {selected && id && (
        <BreakdownDrawer
          id={id}
          slug={slug}
          teamId={selected}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}

function BreakdownDrawer({
  id,
  slug,
  teamId,
  onClose,
}: {
  id: string
  slug: string
  teamId: string
  onClose: () => void
}) {
  const detail = useQuery({
    queryKey: ['team-detail', id, teamId],
    queryFn: () => api.get<TeamDetailResponse>(`/hackathons/${id}/leaderboard/${teamId}`),
  })

  return (
    <Drawer open onOpenChange={(o) => !o && onClose()} placement="right">
      <div className="hs-drawer-content" style={{ maxWidth: 560 }}>
        <div className="hs-justify-between">
          <Text variant="header-2">{detail.data?.team.team_name ?? '…'}</Text>
          {detail.data && (
            <Text variant="body-2" color="secondary">
              Балл {detail.data.team.score.toFixed(2)} · место {detail.data.team.rank} ·{' '}
              полнота {detail.data.team.completeness}%
            </Text>
          )}
        </div>

        {detail.isLoading && (
          <Text variant="body-2" color="secondary">
            Загрузка детализации…
          </Text>
        )}
        {detail.data && (
          <>
            <div className="hs-col" style={{ gap: 0 }}>
              {detail.data.breakdown.map((b) => (
                <div key={b.key} className="hs-breakdown-item">
                  <div className="hs-justify-between">
                    <Text variant="body-2">{b.name}</Text>
                    <Label
                      size="s"
                      theme={
                        b.status === 'fresh'
                          ? 'success'
                          : b.status === 'stale'
                            ? 'warning'
                            : b.status === 'missing'
                              ? 'danger'
                              : 'utility'
                      }
                    >
                      {ru(metricStatusRu, b.status)}
                    </Label>
                  </div>
                  <div className="hs-kv-grid">
                    <Text variant="caption-2" color="secondary">
                      значение: {b.raw ?? '—'}
                    </Text>
                    <Text variant="caption-2" color="secondary">
                      баллы: {b.normalized}
                    </Text>
                    <Text variant="caption-2" color="secondary">
                      вес: {b.weight}
                    </Text>
                    <Text variant="caption-2" color="secondary">
                      вклад: {b.contribution}
                    </Text>
                    {b.type === 'manual' ? (
                      <>
                        <Text variant="caption-2" color="secondary">
                          судей: {b.judge_count}
                        </Text>
                        <Text variant="caption-2" color="secondary">
                          исключено выбросов: {b.excluded_count}
                        </Text>
                        <Text variant="caption-2" color="secondary">
                          метод: {b.aggregation_method}
                        </Text>
                      </>
                    ) : (
                      <>
                        <Text variant="caption-2" color="secondary">
                          источник: {b.source || '—'}
                        </Text>
                        <Text variant="caption-2" color="secondary">
                          время замера:{' '}
                          {b.last_timestamp ? new Date(b.last_timestamp).toLocaleString() : '—'}
                        </Text>
                      </>
                    )}
                  </div>
                </div>
              ))}
            </div>
            <Link to="/h/$slug/teams/$teamId" params={{ slug, teamId }} className="hs-nav-link">
              <Text variant="body-2" color="link">
                Открыть страницу команды
              </Text>
            </Link>
          </>
        )}
      </div>
    </Drawer>
  )
}
