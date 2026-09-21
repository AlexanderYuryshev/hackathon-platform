import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Button, Card, Disclosure, Label, Progress, Slider, Text, TextArea } from '@gravity-ui/uikit'
import { api, me, ApiError } from '~/lib/api'
import { roleOf, useMyRoles } from '~/lib/roles'
import { RoleBadge } from '~/components/RoleBadge'
import type { Criterion, Hackathon, MetricsResponse, MyAssignment, SavedScores, Submission } from '~/lib/types'

export const Route = createFileRoute('/judge/$slug')({
  component: JudgePage,
})

function JudgePage() {
  const { slug } = Route.useParams()
  const hackathon = useQuery({
    queryKey: ['hackathon', slug],
    queryFn: () => api.get<Hackathon>(`/hackathons/${slug}`),
  })
  const user = useQuery({ queryKey: ['me'], queryFn: () => me(), retry: false })
  const id = hackathon.data?.id
  const { roles } = useMyRoles()
  const role = roleOf(roles, id)

  const assignments = useQuery({
    queryKey: ['my-assignments', id],
    queryFn: () => api.get<{ assignments: MyAssignment[] }>(`/hackathons/${id}/judging/my-assignments`),
    // Панель судьи — только для судей. Организатор проходит судейский гейт
    // бэкенда по старшинству ролей, но назначений у него нет — не дёргаем API,
    // а сразу показываем объяснение ниже.
    enabled: !!id && user.isSuccess && role !== 'organizer',
    retry: false,
  })

  if (hackathon.isLoading) {
    return (
      <Text variant="body-1" color="secondary" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Загрузка…
      </Text>
    )
  }
  if (user.isError) {
    return (
      <div className="hs-col hs-center" style={{ alignItems: 'center', padding: 'var(--g-spacing-6)', gap: 'var(--g-spacing-3)' }}>
        <Text variant="body-1" color="secondary">
          Войдите как судья.
        </Text>
        <Link to="/login" className="hs-nav-link">
          <Text variant="body-2" color="link">
            Перейти ко входу
          </Text>
        </Link>
      </div>
    )
  }
  if (!id) return null

  // Организатор — не судья: назначений у него нет и быть не должно.
  // Вместо пустой панели отправляем его в консоль, где назначаются судьи.
  if (role === 'organizer') {
    return (
      <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', maxWidth: 720 }}>
        <Text variant="header-1" as="h1">
          Судейство: {hackathon.data?.name}
        </Text>
        <RoleBadge role={role} />
        <Card type="container" view="outlined">
          <div className="hs-col" style={{ gap: 'var(--g-spacing-3)', padding: 'var(--g-spacing-4)' }}>
            <Text variant="subheader-1">Вы — организатор, а не судья</Text>
            <Text variant="body-2" color="secondary">
              Оценки ставят назначенные судьи. Если хотите судить сами — создайте
              отдельного пользователя-судью и назначьте его: судить собственным
              организаторским аккаунтом нельзя.
            </Text>
            <div className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
              <Link to="/app/hackathons/$id" params={{ id }} className="hs-nav-link">
                <Button view="action">Панель организатора</Button>
              </Link>
              <Link to="/h/$slug" params={{ slug }} className="hs-nav-link">
                <Text variant="body-2" color="link">
                  Лидерборд
                </Text>
              </Link>
            </div>
          </div>
        </Card>
      </div>
    )
  }

  const assignmentsError = assignments.error as ApiError | null
  const forbidden = assignmentsError?.status === 403 || assignmentsError?.status === 401
  const rows = assignments.data?.assignments ?? []
  const active = rows.filter((a) => a.status === 'assigned')
  const scored = active.filter((a) => a.scored_criteria >= a.total_criteria && a.total_criteria > 0)
  const progress = active.length ? Math.round((scored.length / active.length) * 100) : 0
  // Group by track when the hackathon has several (criteria differ per track).
  const byTrack = new Map<string, { name: string; items: MyAssignment[] }>()
  for (const a of rows) {
    const g = byTrack.get(a.track_key) ?? { name: a.track_name, items: [] }
    g.items.push(a)
    byTrack.set(a.track_key, g)
  }
  const groups = [...byTrack.values()]

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-5)' }}>
      <div className="hs-col" style={{ gap: 'var(--g-spacing-2)' }}>
        <Text variant="header-1" as="h1">
          Судейство: {hackathon.data?.name}
        </Text>
        <RoleBadge role={role} />
        <Text variant="body-2" color="secondary">
          Оценено команд: {scored.length} из {active.length}
        </Text>
        <div style={{ maxWidth: 280 }}>
          <Progress value={progress} size="s" />
        </div>
      </div>

      {forbidden ? (
        <Card type="container" view="outlined">
          <div className="hs-col" style={{ gap: 'var(--g-spacing-3)', padding: 'var(--g-spacing-4)' }}>
            <Text variant="body-1" color="danger">
              Эта панель доступна только судьям хакатона.
            </Text>
            {role === 'team_member' ? (
              <Text variant="body-2" color="secondary">
                Вы участвуете в этом хакатоне командой — судить его нельзя.
                Ваш проект — в кабинете команды.
              </Text>
            ) : (
              <Text variant="body-2" color="secondary">
                Попросите организатора назначить вас судьёй — после назначения
                команды появятся здесь автоматически.
              </Text>
            )}
            <div className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
              <Link to="/team/$slug" params={{ slug }} className="hs-nav-link">
                <Text variant="body-2" color="link">
                  Кабинет команды
                </Text>
              </Link>
              <Link to="/h/$slug" params={{ slug }} className="hs-nav-link">
                <Text variant="body-2" color="link">
                  Лидерборд
                </Text>
              </Link>
            </div>
          </div>
        </Card>
      ) : (
        <>
          {assignments.isError && (
            <Text variant="body-2" color="danger">
              Не удалось загрузить назначения: {(assignments.error as Error)?.message ?? 'ошибка сети'}
            </Text>
          )}
          {assignments.isSuccess && rows.length === 0 && (
            <Card type="container" view="outlined">
              <div className="hs-col" style={{ gap: 'var(--g-spacing-2)', padding: 'var(--g-spacing-4)' }}>
                <Text variant="body-1">Назначений пока нет</Text>
                <Text variant="body-2" color="secondary">
                  Организатор ещё не назначил вам команды. Загляните позже —
                  список появится здесь сам.
                </Text>
              </div>
            </Card>
          )}

      <div className="hs-col">
        {groups.map((g) => (
          <div key={g.name} className="hs-col" style={{ gap: 'var(--g-spacing-3)' }}>
            {groups.length > 1 && (
              <Text variant="subheader-2" style={{ marginTop: 'var(--g-spacing-2)' }}>
                Трек: {g.name}
              </Text>
            )}
            {g.items.map((a) => (
              <AssignmentCard key={a.team_id} hackathonId={id} assignment={a} />
            ))}
          </div>
        ))}
      </div>
        </>
      )}
    </div>
  )
}

function AssignmentCard({ hackathonId, assignment }: { hackathonId: string; assignment: MyAssignment }) {
  const [open, setOpen] = useState(false)
  const recused = assignment.status === 'recused'

  return (
    <Card type="container" view="outlined">
      <div className="hs-col" style={{ gap: 'var(--g-spacing-3)', padding: 'var(--g-spacing-4)' }}>
        <div className="hs-justify-between">
          <div className="hs-col" style={{ gap: 0 }}>
            <Text variant="subheader-1">{assignment.team_name}</Text>
            <Text variant="body-2" color="secondary">
              {assignment.project_title || 'Работы пока нет'} ·{' '}
              оценено критериев: {assignment.scored_criteria}/{assignment.total_criteria}
            </Text>
          </div>
          <div className="hs-row">
            {!recused && (
              <Button view={open ? 'normal' : 'action'} onClick={() => setOpen(!open)}>
                {open ? 'Закрыть' : 'Оценить'}
              </Button>
            )}
            {recused && (
              <Label theme="utility" size="s">
                Самоотвод
              </Label>
            )}
          </div>
        </div>
        {recused && assignment.recusal_reason && (
          <Text variant="caption-2" color="secondary">
            Причина: {assignment.recusal_reason}
          </Text>
        )}
        {open && !recused && <ScoringForm hackathonId={hackathonId} assignment={assignment} />}
      </div>
    </Card>
  )
}

function ScoringForm({ hackathonId, assignment }: { hackathonId: string; assignment: MyAssignment }) {
  const queryClient = useQueryClient()
  const metrics = useQuery({
    queryKey: ['metrics', hackathonId, assignment.track_key],
    queryFn: () => api.get<MetricsResponse>(`/hackathons/${hackathonId}/metrics?track=${assignment.track_key}`),
  })
  const submission = useQuery({
    queryKey: ['submission', hackathonId, assignment.team_id],
    queryFn: () => api.get<Submission>(`/hackathons/${hackathonId}/submissions/${assignment.team_id}`),
    retry: false,
  })
  const savedScores = useQuery({
    queryKey: ['my-scores', hackathonId, assignment.team_id],
    queryFn: () => api.get<SavedScores>(`/hackathons/${hackathonId}/judging/scores/${assignment.team_id}`),
    retry: false,
  })

  const [values, setValues] = useState<Record<string, number>>({})
  const [comment, setComment] = useState('')
  const [saved, setSaved] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [recusalReason, setRecusalReason] = useState('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const dirty = useRef<Set<string>>(new Set())
  const initialized = useRef(false)

  const criteria: Criterion[] = metrics.data?.criteria ?? []
  const loaded = !!metrics.data && savedScores.isSuccess

  // Initialize the form from the judge's own previously saved scores, so a
  // revisit never overwrites them with scale defaults.
  useEffect(() => {
    if (!metrics.data || !savedScores.data || initialized.current) return
    initialized.current = true
    const prev: Record<string, number> = {}
    for (const s of savedScores.data.scores) {
      prev[s.criterion_key] = s.value
    }
    setValues(prev)
    setComment(savedScores.data.comment ?? '')
  }, [metrics.data, savedScores.data])

  useEffect(() => {
    if (timer.current) clearTimeout(timer.current)
    if (dirty.current.size === 0) return
    timer.current = setTimeout(() => void save(), 1200)
    return () => {
      if (timer.current) clearTimeout(timer.current)
    }
  }, [values, comment])

  async function save() {
    const keys = [...dirty.current]
    if (keys.length === 0) return
    setError(null)
    try {
      const scores = keys.map((key) => ({ criterion_key: key, value: values[key] }))
      await api.put(`/hackathons/${hackathonId}/judging/scores/${assignment.team_id}`, {
        scores,
        comment,
      })
      for (const k of keys) dirty.current.delete(k)
      setSaved(new Date().toLocaleTimeString())
      void queryClient.invalidateQueries({ queryKey: ['my-assignments', hackathonId] })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'не удалось сохранить')
    }
  }

  async function recuse() {
    if (!recusalReason.trim()) {
      setError('Для самоотвода нужна причина')
      return
    }
    try {
      await api.post(`/hackathons/${hackathonId}/judging/recusal`, {
        team_id: assignment.team_id,
        reason: recusalReason,
      })
      void queryClient.invalidateQueries({ queryKey: ['my-assignments', hackathonId] })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'не удалось оформить самоотвод')
    }
  }

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', borderTop: '1px solid var(--g-color-line-generic)', paddingTop: 'var(--g-spacing-4)' }}>
      {submission.data && (
        <div className="hs-col" style={{ gap: 'var(--g-spacing-2)' }}>
          <Text variant="subheader-2">{submission.data.title}</Text>
          <Text variant="body-2" color="secondary">
            {submission.data.description}
          </Text>
          <div className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
            {submission.data.repo_url && (
              <a href={submission.data.repo_url} className="hs-nav-link">
                <Text variant="body-2" color="link">
                  репозиторий
                </Text>
              </a>
            )}
            {submission.data.demo_url && (
              <a href={submission.data.demo_url} className="hs-nav-link">
                <Text variant="body-2" color="link">
                  демо
                </Text>
              </a>
            )}
            {submission.data.app_url && (
              <a href={submission.data.app_url} className="hs-nav-link">
                <Text variant="body-2" color="link">
                  приложение
                </Text>
              </a>
            )}
          </div>
        </div>
      )}

      <div className="hs-col" style={{ gap: 'var(--g-spacing-3)' }}>
        {criteria.map((c) => {
          const step = c.scale_max - c.scale_min <= 10 ? 0.5 : 1
          return (
            <div key={c.key} className="hs-slider-row">
              <Text variant="body-2" ellipsis>
                {c.name}
                <Text variant="caption-2" color="hint" as="span">
                  {' '}
                  ({c.weight})
                </Text>
              </Text>
              <Slider
                min={c.scale_min}
                max={c.scale_max}
                step={step}
                disabled={!loaded}
                value={values[c.key] ?? c.scale_min}
                onUpdate={(v) => {
                  const n = Array.isArray(v) ? v[0] : v
                  dirty.current.add(c.key)
                  setValues((prev) => ({ ...prev, [c.key]: n }))
                }}
              />
              <Text variant="code-inline-2" className="hs-num">
                {values[c.key] ?? c.scale_min}
              </Text>
            </div>
          )
        })}
      </div>

      <div>
        <Text variant="body-2" color="secondary">
          Комментарий (по желанию)
        </Text>
        <TextArea value={comment} onChange={(e) => setComment(e.target.value)} minRows={2} />
      </div>

      <div className="hs-row">
        <Button view="action" disabled={!loaded || dirty.current.size === 0} onClick={() => void save()}>
          Сохранить оценки
        </Button>
        {saved && (
          <Text variant="caption-2" color="positive">
            автосохранено в {saved}
          </Text>
        )}
        {error && (
          <Text variant="caption-2" color="danger">
            {error}
          </Text>
        )}
      </div>

      <Disclosure
        summary={
          <Text variant="body-2" color="secondary">
            Конфликт интересов? Заявите самоотвод
          </Text>
        }
      >
        <div className="hs-row" style={{ marginTop: 'var(--g-spacing-2)' }}>
          <div className="hs-grow">
            <TextArea
              value={recusalReason}
              onChange={(e) => setRecusalReason(e.target.value)}
              placeholder="Причина…"
              minRows={1}
            />
          </div>
          <Button view="outlined-danger" onClick={() => void recuse()}>
            Самоотвод
          </Button>
        </div>
      </Disclosure>
    </div>
  )
}
