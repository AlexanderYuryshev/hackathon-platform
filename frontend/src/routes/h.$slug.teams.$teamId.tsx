import { createFileRoute, Link, useParams } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { TableColumnConfig } from '@gravity-ui/uikit'
import { Card, Label, Table, Text } from '@gravity-ui/uikit'
import { api, ssrRequestHeaders } from '~/lib/api'
import { metricStatusRu, ru, scoreStatusRu } from '~/lib/labels'
import type { BreakdownItem, Hackathon, Submission, TeamDetailResponse } from '~/lib/types'
import { LineChart } from '~/components/charts'

export const Route = createFileRoute('/h/$slug/teams/$teamId')({
  loader: async ({ params, context: { queryClient } }) => {
    const ssrHeaders = await ssrRequestHeaders()
    const hackathon = await queryClient.ensureQueryData({
      queryKey: ['hackathon', params.slug],
      queryFn: () => api.get<Hackathon>(`/hackathons/${params.slug}`, ssrHeaders),
    })
    await queryClient.ensureQueryData({
      queryKey: ['team-detail', hackathon.id, params.teamId],
      queryFn: () =>
        api.get<TeamDetailResponse>(`/hackathons/${hackathon.id}/leaderboard/${params.teamId}`, ssrHeaders),
    })
  },
  component: TeamPage,
})

const breakdownColumns: TableColumnConfig<BreakdownItem>[] = [
  { id: 'name', name: 'Метрика' },
  {
    id: 'raw',
    name: 'Значение',
    align: 'end',
    template: (b) => <Text variant="code-inline-2">{b.raw ?? '—'}</Text>,
  },
  {
    id: 'normalized',
    name: 'Баллы',
    align: 'end',
    template: (b) => <Text variant="code-inline-2">{b.normalized}</Text>,
  },
  {
    id: 'weight',
    name: 'Вес',
    align: 'end',
    template: (b) => <Text variant="code-inline-2">{b.weight}</Text>,
  },
  {
    id: 'contribution',
    name: 'Вклад',
    align: 'end',
    template: (b) => <Text variant="code-inline-2">{b.contribution}</Text>,
  },
  {
    id: 'status',
    name: 'Статус',
    template: (b) => (
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
    ),
  },
  {
    id: 'source',
    name: 'Источник',
    template: (b) => (
      <Text variant="caption-2" color="secondary">
        {b.source || '—'}
      </Text>
    ),
  },
  {
    id: 'judges',
    name: 'Судьи',
    align: 'end',
    template: (b) => (
      <Text variant="code-inline-2">
        {b.type === 'manual'
          ? `${b.judge_count}${b.excluded_count ? ` (${b.excluded_count} искл.)` : ''}`
          : '—'}
      </Text>
    ),
  },
]

function TeamPage() {
  const { slug, teamId } = useParams({ from: '/h/$slug/teams/$teamId' })
  const hackathon = useQuery({
    queryKey: ['hackathon', slug],
    queryFn: () => api.get<Hackathon>(`/hackathons/${slug}`),
  })
  const id = hackathon.data?.id
  const detail = useQuery({
    queryKey: ['team-detail', id, teamId],
    queryFn: () => api.get<TeamDetailResponse>(`/hackathons/${id}/leaderboard/${teamId}`),
    enabled: !!id,
  })
  const submission = useQuery({
    queryKey: ['submission', id, teamId],
    queryFn: () => api.get<Submission>(`/hackathons/${id}/submissions/${teamId}`),
    enabled: !!id,
    retry: false,
  })

  if (!id || detail.isLoading) {
    return (
      <Text variant="body-1" color="secondary" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Загрузка…
      </Text>
    )
  }
  if (detail.isError || !detail.data) {
    return (
      <Text variant="body-1" color="danger" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Команда не найдена
      </Text>
    )
  }

  const { team, breakdown, history } = detail.data

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-6)' }}>
      <div className="hs-col" style={{ gap: 0 }}>
        <Link to="/h/$slug" params={{ slug }} className="hs-nav-link">
          <Text variant="body-2" color="link">
            Лидерборд
          </Text>
        </Link>
        <Text variant="header-1" as="h1" style={{ marginTop: 'var(--g-spacing-2)' }}>
          {team.team_name}
        </Text>
        <Text variant="body-2" color="secondary">
          Трек: {team.track || '—'} · Балл {team.score.toFixed(2)} · место {team.rank} ·{' '}
          полнота {team.completeness}% · {ru(scoreStatusRu, team.status)}
        </Text>
      </div>

      {submission.data && (
        <Card type="container" view="outlined">
          <div className="hs-col" style={{ gap: 'var(--g-spacing-2)', padding: 'var(--g-spacing-4)' }}>
            <Text variant="subheader-1">
              Проект: {submission.data.title}{' '}
              <Text variant="caption-2" color="hint" as="span">
                v{submission.data.version}
              </Text>
            </Text>
            <Text variant="body-2" color="secondary">
              {submission.data.description || 'Без описания'}
            </Text>
            <div className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
              {submission.data.repo_url && (
                <a href={submission.data.repo_url} className="hs-nav-link">
                  <Text variant="body-2" color="link">
                    Репозиторий
                  </Text>
                </a>
              )}
              {submission.data.demo_url && (
                <a href={submission.data.demo_url} className="hs-nav-link">
                  <Text variant="body-2" color="link">
                    Демо
                  </Text>
                </a>
              )}
              {submission.data.app_url && (
                <a href={submission.data.app_url} className="hs-nav-link">
                  <Text variant="body-2" color="link">
                    Приложение
                  </Text>
                </a>
              )}
            </div>
          </div>
        </Card>
      )}

      <div className="hs-col" style={{ gap: 'var(--g-spacing-3)' }}>
        <Text variant="subheader-2">История баллов</Text>
        <LineChart values={history.map((h) => ({ score: h.score, captured_at: h.captured_at }))} />
      </div>

      <div className="hs-col" style={{ gap: 'var(--g-spacing-3)' }}>
        <Text variant="subheader-2">Оценка по метрикам</Text>
        <Table data={breakdown} columns={breakdownColumns} getRowId={(b) => b.key} />
      </div>
    </div>
  )
}
