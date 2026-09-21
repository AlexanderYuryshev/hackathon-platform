import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { TableColumnConfig } from '@gravity-ui/uikit'
import {
  Button,
  Card,
  Checkbox,
  ClipboardButton,
  Label,
  Modal,
  Select,
  Tab,
  TabList,
  TabPanel,
  TabProvider,
  Table,
  Text,
  TextArea,
  TextInput,
} from '@gravity-ui/uikit'
import { toaster } from '@gravity-ui/uikit/toaster-singleton'
import { api, me, ApiError } from '~/lib/api'
import { defaultTrackKey, useTracks } from '~/lib/tracks'
import { roleOf, useMyRoles } from '~/lib/roles'
import { RoleBadge } from '~/components/RoleBadge'
import { assignmentStatusRu, hackathonStatusRu, roleRu, ru, runModeRu, runStatusRu } from '~/lib/labels'
import type {
  Assignment,
  AuditEvent,
  Hackathon,
  IngestSource,
  MetricsResponse,
  ScoreRun,
  Team,
  Track,
} from '~/lib/types'

export const Route = createFileRoute('/app/hackathons/$id')({
  component: OrganizerConsole,
})

const TABS = [
  'overview',
  'teams',
  'metrics',
  'rubric',
  'judges',
  'ingest',
  'runs',
  'audit',
  'finalize',
] as const

type TabValue = (typeof TABS)[number]

const tabLabels: Record<TabValue, string> = {
  overview: 'Обзор',
  teams: 'Команды',
  metrics: 'Метрики',
  rubric: 'Критерии',
  judges: 'Судьи',
  ingest: 'Сбор данных',
  runs: 'Пересчёты',
  audit: 'Аудит',
  finalize: 'Финализация',
}

function OrganizerConsole() {
  const { id } = Route.useParams()
  const [tab, setTab] = useState<TabValue>('overview')
  const [pickedTrack, setPickedTrack] = useState<string | null>(null)
  const user = useQuery({ queryKey: ['me'], queryFn: () => me(), retry: false })
  const hackathon = useQuery({
    queryKey: ['hackathon-id', id],
    queryFn: () => api.get<Hackathon>(`/hackathons/${id}`),
  })
  // Права определяет бэкенд: /audit доступен только организатору.
  const access = useQuery({
    queryKey: ['access-organizer', id],
    queryFn: () => api.get(`/hackathons/${id}/audit?limit=1`),
    enabled: user.isSuccess,
    retry: false,
  })
  const tracksQuery = useTracks(user.isSuccess ? id : undefined)
  const tracks = tracksQuery.data?.tracks ?? []
  const trackKey = pickedTrack ?? defaultTrackKey(tracks)
  const track = tracks.find((t) => t.key === trackKey)
  const { roles } = useMyRoles()
  const role = roleOf(roles, id)

  if (user.isError) {
    return (
      <div className="hs-col hs-center" style={{ alignItems: 'center', padding: 'var(--g-spacing-6)', gap: 'var(--g-spacing-3)' }}>
        <Text variant="body-1" color="secondary">
          Войдите как организатор.
        </Text>
        <Link to="/login" className="hs-nav-link">
          <Text variant="body-2" color="link">
            Перейти ко входу
          </Text>
        </Link>
      </div>
    )
  }
  if (hackathon.isError) {
    return (
      <Text variant="body-1" color="danger" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Хакатон не найден.
      </Text>
    )
  }
  if (access.isPending || !hackathon.data) {
    return (
      <Text variant="body-1" color="secondary" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Загрузка…
      </Text>
    )
  }
  if (access.isError && (access.error as ApiError)?.status !== 404) {
    const slug = hackathon.data.slug
    return (
      <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', maxWidth: 720 }}>
        <Text variant="header-1" as="h1">
          {hackathon.data.name}
        </Text>
        <RoleBadge role={role} />
        <Card type="container" view="outlined">
          <div className="hs-col" style={{ gap: 'var(--g-spacing-3)', padding: 'var(--g-spacing-4)' }}>
            <Text variant="subheader-1">Это панель организатора</Text>
            <Text variant="body-2" color="secondary">
              {role === 'judge'
                ? 'Вы судите этот хакатон — управляют им из другого места. Ваше рабочее место — панель судьи.'
                : role === 'team_member'
                  ? 'Вы участвуете в этом хакатоне командой — управляет им организатор. Ваш проект — в кабинете команды.'
                  : 'Управлять хакатоном может только его организатор. Если это ваш хакатон, войдите под аккаунтом организатора.'}
            </Text>
            <div className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
              <Link to="/h/$slug" params={{ slug }} className="hs-nav-link">
                <Button view="action">Лидерборд</Button>
              </Link>
              {role === 'judge' && (
                <Link to="/judge/$slug" params={{ slug }} className="hs-nav-link">
                  <Text variant="body-2" color="link">
                    Панель судьи
                  </Text>
                </Link>
              )}
              {(role === 'team_member' || role === undefined) && (
                <Link to="/team/$slug" params={{ slug }} className="hs-nav-link">
                  <Text variant="body-2" color="link">
                    Кабинет команды
                  </Text>
                </Link>
              )}
            </div>
          </div>
        </Card>
      </div>
    )
  }
  const h = hackathon.data
  const frozen = (track?.scoring_locked ?? false) || h.status === 'finalized'

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)' }}>
      <div className="hs-justify-between">
        <Text variant="header-1" as="h1">
          {h.name}
        </Text>
        <div className="hs-row">
          {tracks.length > 1 && (
            <Select
              label="Трек"
              value={trackKey ? [trackKey] : []}
              onUpdate={(v) => setPickedTrack(Array.isArray(v) ? (v[0] ?? null) : (v ?? null))}
              options={tracks.map((t) => ({ value: t.key, content: t.name }))}
              className="hs-w-select"
            />
          )}
          <Link to="/h/$slug" params={{ slug: h.slug }} className="hs-nav-link">
            <Button view="outlined-action">Публичный лидерборд</Button>
          </Link>
          <Link to="/hackathons/new" className="hs-nav-link">
            <Button view="outlined">Новый хакатон</Button>
          </Link>
        </div>
      </div>

      <TabProvider value={tab} onUpdate={(v) => setTab(v as TabValue)}>
        <TabList>
          {TABS.map((t) => (
            <Tab key={t} value={t}>
              {tabLabels[t]}
            </Tab>
          ))}
        </TabList>
        <TabPanel value="overview">
          <Overview id={id} track={track} />
        </TabPanel>
        <TabPanel value="teams">
          <TeamsTab id={id} trackKey={trackKey} tracks={tracks} />
        </TabPanel>
        <TabPanel value="metrics">
          <MetricsTab id={id} trackKey={trackKey} frozen={frozen} />
        </TabPanel>
        <TabPanel value="rubric">
          <RubricTab id={id} trackKey={trackKey} frozen={frozen} />
        </TabPanel>
        <TabPanel value="judges">
          <JudgesTab id={id} trackKey={trackKey} frozen={h.status === 'finalized'} />
        </TabPanel>
        <TabPanel value="ingest">
          <IngestTab id={id} />
        </TabPanel>
        <TabPanel value="runs">
          <RunsTab id={id} trackKey={trackKey} />
        </TabPanel>
        <TabPanel value="audit">
          <AuditTab id={id} />
        </TabPanel>
        <TabPanel value="finalize">
          <FinalizeTab id={id} />
        </TabPanel>
      </TabProvider>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card type="container" view="outlined">
      <div className="hs-col" style={{ gap: 'var(--g-spacing-1)', padding: 'var(--g-spacing-4)' }}>
        <Text variant="caption-1" color="secondary">
          {label.toUpperCase()}
        </Text>
        <Text variant="subheader-2">{value}</Text>
      </div>
    </Card>
  )
}

function Overview({ id, track }: { id: string; track: Track | undefined }) {
  const hackathon = useQuery({
    queryKey: ['hackathon-id', id],
    queryFn: () => api.get<Hackathon>(`/hackathons/${id}`),
  })
  const teams = useQuery({
    queryKey: ['teams', id, track?.key ?? ''],
    queryFn: () => api.get<{ teams: Team[] }>(`/hackathons/${id}/teams${track ? `?track=${track.key}` : ''}`),
    enabled: !!track,
  })
  const h = hackathon.data
  if (!h) return null
  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)' }}>
      <div className="hs-grid hs-grid-stats">
        <Stat label="Статус" value={ru(hackathonStatusRu, h.status)} />
        <Stat label="Команды" value={String(teams.data?.teams.length ?? '…')} />
        <Stat label="Версия скоринга" value={track ? `v${track.scoring_version}${track.scoring_locked ? ' (заблокирована)' : ''}` : '…'} />
        <Stat label="Судей на команду (мин.)" value={track ? String(track.min_judges_per_team) : '…'} />
        <Stat label="Начало" value={h.starts_at ? new Date(h.starts_at).toLocaleString() : '—'} />
        <Stat label="Дедлайн проектов" value={h.ends_at ? new Date(h.ends_at).toLocaleString() : '—'} />
        <Stat label="Судейство до" value={h.judging_ends_at ? new Date(h.judging_ends_at).toLocaleString() : '—'} />
        <Stat label="Финализация" value={h.finalized_at ? new Date(h.finalized_at).toLocaleString() : '—'} />
      </div>
      {track && <StatusControls id={id} hackathon={h} trackKey={track.key} locked={track.scoring_locked} />}
    </div>
  )
}

const statusTransitions: Record<string, { to: string; label: string }[]> = {
  draft: [{ to: 'running', label: 'Запустить хакатон' }],
  running: [{ to: 'judging', label: 'Закрыть приём работ и открыть судейство' }],
  judging: [{ to: 'running', label: 'Открыть приём работ заново' }],
}

function PromptModal({
  open,
  title,
  hint,
  confirmLabel,
  danger,
  withReason,
  onConfirm,
  onClose,
}: {
  open: boolean
  title: string
  hint?: string
  confirmLabel: string
  danger?: boolean
  withReason?: boolean
  onConfirm: (reason: string) => void
  onClose: () => void
}) {
  const [reason, setReason] = useState('')
  return (
    <Modal open={open} onClose={onClose}>
      <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', padding: 'var(--g-spacing-5)', minWidth: 380 }}>
        <Text variant="subheader-1">{title}</Text>
        {hint && (
          <Text variant="body-2" color="secondary">
            {hint}
          </Text>
        )}
        {withReason && (
          <div>
            <Text variant="body-2" color="secondary">
              Причина (будет записана в аудит)
            </Text>
            <TextArea value={reason} onChange={(e) => setReason(e.target.value)} minRows={2} />
          </div>
        )}
        <div className="hs-row" style={{ justifyContent: 'flex-end' }}>
          <Button view="flat-secondary" onClick={onClose}>
            Отмена
          </Button>
          <Button
            view={danger ? 'outlined-danger' : 'action'}
            onClick={() => {
              onConfirm(reason.trim())
              setReason('')
              onClose()
            }}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function StatusControls({ id, hackathon, trackKey, locked }: { id: string; hackathon: Hackathon; trackKey: string; locked: boolean }) {
  const queryClient = useQueryClient()
  const [prompt, setPrompt] = useState<{ kind: 'status' | 'lock'; to?: string } | null>(null)
  const options = statusTransitions[hackathon.status] ?? []

  async function setStatus(to: string, reason: string) {
    try {
      await api.post(`/hackathons/${id}/status`, { status: to, reason })
      toaster.add({ name: 'toast-1', title: `Статус: ${ru(hackathonStatusRu, to)}`, theme: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['hackathon-id', id] })
    } catch (err) {
      toaster.add({ name: 'toast-2', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    }
  }

  async function toggleLock(reason: string) {
    const next = !locked
    try {
      await api.post(`/hackathons/${id}/scoring/lock?track=${trackKey}`, { locked: next, reason })
      toaster.add({ name: 'toast-3', title: next ? 'Скоринг заблокирован' : 'Скоринг разблокирован', theme: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['tracks', id] })
    } catch (err) {
      toaster.add({ name: 'toast-4', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    }
  }

  return (
    <Card type="container" view="outlined">
      <div className="hs-row" style={{ padding: 'var(--g-spacing-4)' }}>
        <Text variant="body-2">Жизненный цикл:</Text>
        {options.map((o) => (
          <Button key={o.to} view="action" onClick={() => setPrompt({ kind: 'status', to: o.to })}>
            {o.label}
          </Button>
        ))}
        {hackathon.status !== 'finalized' && (
          <Button view="outlined-warning" onClick={() => setPrompt({ kind: 'lock' })}>
            {locked ? 'Разблокировать скоринг' : 'Заблокировать скоринг'}
          </Button>
        )}
        {hackathon.status === 'finalized' && (
          <Text variant="body-2" color="secondary">
            Хакатон финализирован, изменения невозможны.
          </Text>
        )}
      </div>

      {prompt?.kind === 'status' && prompt.to && (
        <PromptModal
          open
          title={`Подтверждение: ${statusTransitions[hackathon.status]?.find((o) => o.to === prompt.to)?.label ?? prompt.to}`}
          withReason
          confirmLabel="Применить"
          onConfirm={(reason) => void setStatus(prompt.to!, reason)}
          onClose={() => setPrompt(null)}
        />
      )}
      {prompt?.kind === 'lock' && (
        <PromptModal
          open
          title={locked ? 'Разблокировать скоринг?' : 'Заблокировать скоринг?'}
          hint="Пока заблокировано, живой пересчёт приостановлен; финализация всё равно работает."
          withReason
          confirmLabel={locked ? 'Разблокировать' : 'Заблокировать'}
          danger={!locked}
          onConfirm={(reason) => void toggleLock(reason)}
          onClose={() => setPrompt(null)}
        />
      )}
    </Card>
  )
}

const teamColumns: TableColumnConfig<Team>[] = [
  { id: 'name', name: 'Команда' },
  { id: 'member_count', name: 'Участников', align: 'end' },
]

function TeamsTab({ id, trackKey, tracks }: { id: string; trackKey: string; tracks: Track[] }) {
  const queryClient = useQueryClient()
  const teams = useQuery({
    queryKey: ['teams', id, trackKey],
    queryFn: () => api.get<{ teams: Team[] }>(`/hackathons/${id}/teams${trackKey ? `?track=${trackKey}` : ''}`),
    enabled: !!trackKey,
  })
  const [name, setName] = useState('')
  const [key, setKey] = useState('')
  const [minJudges, setMinJudges] = useState('3')
  const [busy, setBusy] = useState(false)

  async function createTrack(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setBusy(true)
    try {
      await api.post(`/hackathons/${id}/tracks`, {
        key: key.trim() || undefined,
        name: name.trim(),
        min_judges_per_team: Number(minJudges) || 3,
      })
      setName('')
      setKey('')
      toaster.add({ name: 'toast-track', title: 'Трек создан', theme: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['tracks', id] })
    } catch (err) {
      toaster.add({ name: 'toast-track-err', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    } finally {
      setBusy(false)
    }
  }

  const trackColumns: TableColumnConfig<Track>[] = [
    { id: 'name', name: 'Трек' },
    { id: 'key', name: 'Ключ', template: (t) => <Text variant="code-inline-2">{t.key}</Text> },
    { id: 'team_count', name: 'Команд', align: 'end' },
    { id: 'min_judges_per_team', name: 'Судей (мин.)', align: 'end' },
    {
      id: 'scoring_version',
      name: 'Версия',
      template: (t) => (
        <Text variant="body-2">
          v{t.scoring_version}
          {t.scoring_locked ? ' (заблокирована)' : ''}
        </Text>
      ),
    },
  ]

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)' }}>
      <div className="hs-col" style={{ gap: 'var(--g-spacing-2)' }}>
        <Text variant="subheader-2">Треки</Text>
        <Text variant="body-2" color="secondary">
          У каждого трека свои метрики, критерии и лидерборд. Команда всегда в одном треке.
        </Text>
        <Table data={tracks} columns={trackColumns} getRowId={(t) => t.id} />
        <form onSubmit={createTrack} className="hs-row hs-row-end">
          <TextInput
            label="Название трека"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Мобильные приложения"
          />
          <TextInput
            label="Ключ (необязательно)"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="mobile"
          />
          <TextInput
            label="Минимум судей"
            value={minJudges}
            onChange={(e) => setMinJudges(e.target.value)}
            controlProps={{ type: 'number', min: 1 }}
            style={{ maxWidth: 160 }}
          />
          <Button type="submit" view="outlined-action" disabled={busy || !name.trim()} loading={busy}>
            Добавить трек
          </Button>
        </form>
      </div>
      <Table data={teams.data?.teams ?? []} columns={teamColumns} getRowId={(t) => t.id} />
    </div>
  )
}

function MetricsTab({ id, trackKey, frozen }: { id: string; trackKey: string; frozen: boolean }) {
  const metrics = useQuery({
    queryKey: ['metrics', id, trackKey],
    queryFn: () => api.get<MetricsResponse>(`/hackathons/${id}/metrics${trackKey ? `?track=${trackKey}` : ''}`),
    enabled: !!trackKey,
  })
  const columns: TableColumnConfig<MetricsResponse['metrics'][number]>[] = [
    { id: 'key', name: 'Ключ', template: (m) => <Text variant="code-inline-2">{m.key}</Text> },
    { id: 'name', name: 'Название' },
    { id: 'type', name: 'Тип' },
    { id: 'weight', name: 'Вес', align: 'end', template: (m) => <Text variant="body-2">{m.weight}</Text> },
    {
      id: 'direction',
      name: 'Направление',
      template: (m) => <Text variant="body-2">{m.direction === 'lower_is_better' ? 'меньше — лучше' : 'больше — лучше'}</Text>,
    },
    {
      id: 'public',
      name: 'Публичная',
      template: (m) => (
        <Label size="s" theme={m.public ? 'success' : 'utility'}>
          {m.public ? 'да' : 'нет'}
        </Label>
      ),
    },
  ]
  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)' }}>
      <AddMetricForm id={id} trackKey={trackKey} frozen={frozen} />
      <Table data={metrics.data?.metrics ?? []} columns={columns} getRowId={(m) => m.id} />
    </div>
  )
}

function AddMetricForm({ id, trackKey, frozen }: { id: string; trackKey: string; frozen: boolean }) {
  const queryClient = useQueryClient()
  const [key, setKey] = useState('')
  const [name, setName] = useState('')
  const [weight, setWeight] = useState('10')
  const [direction, setDirection] = useState('higher_is_better')
  const [kind, setKind] = useState('range')
  const [min, setMin] = useState('0')
  const [max, setMax] = useState('100')
  const [target, setTarget] = useState('300')
  const [lower, setLower] = useState('100')
  const [upper, setUpper] = useState('1500')
  const [required, setRequired] = useState(true)
  const [isPublic, setIsPublic] = useState(true)
  const [staleness, setStaleness] = useState('1800')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    const norm: Record<string, unknown> = { kind }
    if (kind === 'range') {
      norm.min = Number(min)
      norm.max = Number(max)
    } else if (kind === 'target') {
      norm.target = Number(target)
      norm.lower = Number(lower)
      norm.upper = Number(upper)
    }
    setBusy(true)
    try {
      await api.post(`/hackathons/${id}/metrics`, {
        key: key.trim().toLowerCase(),
        name: name.trim(),
        type: 'automated',
        weight: Number(weight),
        direction,
        normalization: norm,
        required,
        public: isPublic,
        staleness_seconds: Number(staleness),
        track: trackKey || undefined,
      })
      setKey('')
      setName('')
      toaster.add({ name: 'toast-5', title: 'Метрика добавлена', theme: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['metrics', id, trackKey] })
    } catch (err) {
      toaster.add({ name: 'toast-6', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    } finally {
      setBusy(false)
    }
  }

  return (
    <details className="hs-card-content">
      <summary style={{ cursor: 'pointer' }}>
        <Text variant="body-2">Добавить автоматическую метрику</Text>
      </summary>
      {frozen && (
        <Text variant="caption-2" color="warning">
          Скоринг заблокирован или хакатон финализирован — конфигурация только для чтения.
        </Text>
      )}
      <form onSubmit={onSubmit} className="hs-col" style={{ gap: 'var(--g-spacing-3)' }}>
        <div className="hs-grid-form">
          <TextInput label="Ключ" value={key} onChange={(e) => setKey(e.target.value)} placeholder="api_latency" controlProps={{ required: true }} />
          <TextInput label="Название" value={name} onChange={(e) => setName(e.target.value)} placeholder="API p95 latency" controlProps={{ required: true }} />
          <TextInput label="Вес" value={weight} onChange={(e) => setWeight(e.target.value)} controlProps={{ type: 'number', min: 0, step: 0.5, required: true }} />
          <Select
            label="Направление"
            value={[direction]}
            onUpdate={(v) => setDirection(Array.isArray(v) ? (v[0] ?? direction) : v)}
            options={[
              { value: 'higher_is_better', content: 'больше — лучше' },
              { value: 'lower_is_better', content: 'меньше — лучше' },
            ]}
          />
          <Select
            label="Нормализация"
            value={[kind]}
            onUpdate={(v) => setKind(Array.isArray(v) ? (v[0] ?? kind) : v)}
            options={[
              { value: 'range', content: 'диапазон' },
              { value: 'target', content: 'цель' },
              { value: 'binary', content: 'бинарная' },
            ]}
          />
          {kind === 'range' && (
            <>
              <TextInput label="Мин." value={min} onChange={(e) => setMin(e.target.value)} controlProps={{ type: 'number', required: true }} />
              <TextInput label="Макс." value={max} onChange={(e) => setMax(e.target.value)} controlProps={{ type: 'number', required: true }} />
            </>
          )}
          {kind === 'target' && (
            <>
              <TextInput label="Цель" value={target} onChange={(e) => setTarget(e.target.value)} controlProps={{ type: 'number', required: true }} />
              <TextInput label="Нижняя граница" value={lower} onChange={(e) => setLower(e.target.value)} controlProps={{ type: 'number', required: true }} />
              <TextInput label="Верхняя граница" value={upper} onChange={(e) => setUpper(e.target.value)} controlProps={{ type: 'number', required: true }} />
            </>
          )}
          <TextInput label="Устаревание, сек" value={staleness} onChange={(e) => setStaleness(e.target.value)} controlProps={{ type: 'number', min: 0 }} />
        </div>
        <div className="hs-row">
          <Checkbox checked={required} onUpdate={setRequired}>обязательная</Checkbox>
          <Checkbox checked={isPublic} onUpdate={setIsPublic}>публичная</Checkbox>
          <Button type="submit" view="action" disabled={busy || frozen} loading={busy}>
            Добавить метрику
          </Button>
        </div>
      </form>
    </details>
  )
}

function RubricTab({ id, trackKey, frozen }: { id: string; trackKey: string; frozen: boolean }) {
  const metrics = useQuery({
    queryKey: ['metrics', id, trackKey],
    queryFn: () => api.get<MetricsResponse>(`/hackathons/${id}/metrics${trackKey ? `?track=${trackKey}` : ''}`),
    enabled: !!trackKey,
  })
  const columns: TableColumnConfig<MetricsResponse['criteria'][number]>[] = [
    { id: 'key', name: 'Ключ', template: (c) => <Text variant="code-inline-2">{c.key}</Text> },
    { id: 'name', name: 'Критерий' },
    {
      id: 'scale',
      name: 'Шкала',
      template: (c) => (
        <Text variant="body-2">
          {c.scale_min}–{c.scale_max}
        </Text>
      ),
    },
    { id: 'weight', name: 'Вес', align: 'end', template: (c) => <Text variant="body-2">{c.weight}</Text> },
  ]
  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)' }}>
      <AddCriterionForm id={id} trackKey={trackKey} frozen={frozen} />
      <Table data={metrics.data?.criteria ?? []} columns={columns} getRowId={(c) => c.id} />
    </div>
  )
}

function AddCriterionForm({ id, trackKey, frozen }: { id: string; trackKey: string; frozen: boolean }) {
  const queryClient = useQueryClient()
  const [key, setKey] = useState('')
  const [name, setName] = useState('')
  const [scaleMin, setScaleMin] = useState('0')
  const [scaleMax, setScaleMax] = useState('10')
  const [weight, setWeight] = useState('20')
  const [required, setRequired] = useState(true)
  const [isPublic, setIsPublic] = useState(true)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api.post(`/hackathons/${id}/rubric/criteria`, {
        key: key.trim().toLowerCase(),
        name: name.trim(),
        scale_min: Number(scaleMin),
        scale_max: Number(scaleMax),
        weight: Number(weight),
        required,
        public: isPublic,
        track: trackKey || undefined,
      })
      setKey('')
      setName('')
      toaster.add({ name: 'toast-7', title: 'Критерий добавлен', theme: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['metrics', id, trackKey] })
    } catch (err) {
      toaster.add({ name: 'toast-8', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    } finally {
      setBusy(false)
    }
  }

  return (
    <details className="hs-card-content">
      <summary style={{ cursor: 'pointer' }}>
        <Text variant="body-2">Добавить критерий</Text>
      </summary>
      {frozen && (
        <Text variant="caption-2" color="warning">
          Скоринг заблокирован или хакатон финализирован — конфигурация только для чтения.
        </Text>
      )}
      <form onSubmit={onSubmit} className="hs-col" style={{ gap: 'var(--g-spacing-3)' }}>
        <div className="hs-grid-form">
          <TextInput label="Ключ" value={key} onChange={(e) => setKey(e.target.value)} placeholder="tech_execution" controlProps={{ required: true }} />
          <TextInput label="Название" value={name} onChange={(e) => setName(e.target.value)} placeholder="Technical execution" controlProps={{ required: true }} />
          <TextInput label="Мин. шкалы" value={scaleMin} onChange={(e) => setScaleMin(e.target.value)} controlProps={{ type: 'number', required: true }} />
          <TextInput label="Макс. шкалы" value={scaleMax} onChange={(e) => setScaleMax(e.target.value)} controlProps={{ type: 'number', required: true }} />
          <TextInput label="Вес" value={weight} onChange={(e) => setWeight(e.target.value)} controlProps={{ type: 'number', min: 0, step: 0.5, required: true }} />
        </div>
        <div className="hs-row">
          <Checkbox checked={required} onUpdate={setRequired}>обязательный</Checkbox>
          <Checkbox checked={isPublic} onUpdate={setIsPublic}>публичный</Checkbox>
          <Button type="submit" view="action" disabled={busy || frozen} loading={busy}>
            Добавить критерий
          </Button>
        </div>
      </form>
    </details>
  )
}

function JudgesTab({ id, trackKey, frozen }: { id: string; trackKey: string; frozen: boolean }) {
  const queryClient = useQueryClient()
  const assignments = useQuery({
    queryKey: ['assignments', id],
    queryFn: () => api.get<{ assignments: Assignment[] }>(`/hackathons/${id}/judging/assignments`),
  })
  const teams = useQuery({
    queryKey: ['teams', id, trackKey],
    queryFn: () => api.get<{ teams: Team[] }>(`/hackathons/${id}/teams${trackKey ? `?track=${trackKey}` : ''}`),
    enabled: !!trackKey,
  })
  const [judgeEmail, setJudgeEmail] = useState('')
  const [teamId, setTeamId] = useState('')
  const [busy, setBusy] = useState(false)

  async function autoAssign() {
    setBusy(true)
    try {
      const res = await api.post<{ created: number }>(
        `/hackathons/${id}/judging/auto-assign${trackKey ? `?track=${trackKey}` : ''}`,
      )
      toaster.add({ name: 'toast-9', title: `Создано назначений: ${res.created}`, theme: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['assignments', id] })
    } catch (err) {
      toaster.add({ name: 'toast-10', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    } finally {
      setBusy(false)
    }
  }

  async function assignJudge(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api.post(`/hackathons/${id}/judging/assign`, {
        judge_email: judgeEmail.trim(),
        team_id: teamId,
      })
      setJudgeEmail('')
      toaster.add({ name: 'toast-11', title: 'Судья назначен', theme: 'success' })
      void queryClient.invalidateQueries({ queryKey: ['assignments', id] })
    } catch (err) {
      toaster.add({ name: 'toast-12', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    } finally {
      setBusy(false)
    }
  }

  const columns: TableColumnConfig<Assignment>[] = [
    { id: 'judge_name', name: 'Судья' },
    { id: 'team_name', name: 'Команда' },
    {
      id: 'status',
      name: 'Статус',
      template: (a) => (
        <Label size="s" theme={a.status === 'assigned' ? 'success' : 'utility'}>
          {ru(assignmentStatusRu, a.status)}
        </Label>
      ),
    },
    { id: 'teams_assigned', name: 'Команд у судьи', align: 'end' },
  ]
  const rows = (assignments.data?.assignments ?? []).filter((a) => !trackKey || a.track_key === trackKey)

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)' }}>
      <div className="hs-row hs-row-end">
        <form onSubmit={assignJudge} className="hs-row hs-row-end">
          <TextInput
            label="Email судьи"
            value={judgeEmail}
            onChange={(e) => setJudgeEmail(e.target.value)}
            placeholder="judge1@demo.io"
            controlProps={{ type: 'email', required: true }}
            className="hs-w-select-lg"
          />
          <Select
            label="Команда"
            placeholder="Выберите команду…"
            value={teamId ? [teamId] : []}
            onUpdate={(v) => setTeamId(Array.isArray(v) ? (v[0] ?? '') : (v ?? ''))}
            options={(teams.data?.teams ?? []).map((t) => ({ value: t.id, content: t.name }))}
            className="hs-w-select-lg"
          />
          <Button type="submit" view="outlined-action" disabled={busy || frozen}>
            Назначить
          </Button>
        </form>
        <Button view="action" onClick={() => void autoAssign()} disabled={busy || frozen} loading={busy}>
          Автоназначение судей
        </Button>
      </div>
      <Text variant="caption-2" color="secondary">
        Автоназначение равномерно распределяет судей по командам до минимума на команду;
        сначала назначьте хотя бы одного судью вручную.
      </Text>
      {frozen && (
        <Text variant="caption-2" color="warning">
          Хакатон финализирован — настройки судейства только для чтения.
        </Text>
      )}
      <Table data={rows} columns={columns} getRowId={(a) => `${a.judge_id}-${a.team_id}`} />
    </div>
  )
}

function IngestTab({ id }: { id: string }) {
  const queryClient = useQueryClient()
  const sources = useQuery({
    queryKey: ['ingest-sources', id],
    queryFn: () => api.get<{ sources: IngestSource[] }>(`/hackathons/${id}/ingest-sources`),
  })
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<{ name: string; token: string } | null>(null)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      const res = await api.post<{ name: string; token: string }>(`/hackathons/${id}/ingest-sources`, {
        name: name.trim() || 'collector',
      })
      setCreated(res)
      setName('')
      void queryClient.invalidateQueries({ queryKey: ['ingest-sources', id] })
    } catch (err) {
      toaster.add({ name: 'toast-13', title: 'Ошибка', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    } finally {
      setBusy(false)
    }
  }

  const columns: TableColumnConfig<IngestSource>[] = [
    { id: 'name', name: 'Название' },
    { id: 'kind', name: 'Тип' },
    {
      id: 'last_seen_at',
      name: 'Последняя активность',
      template: (s) => (
        <Text variant="body-2">{s.last_seen_at ? new Date(s.last_seen_at).toLocaleString() : 'никогда'}</Text>
      ),
    },
    {
      id: 'created_at',
      name: 'Создан',
      template: (s) => <Text variant="body-2">{new Date(s.created_at).toLocaleString()}</Text>,
    },
  ]

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)' }}>
      <Text variant="body-2" color="secondary">
        Источники подключаются по bearer-токену и отправляют метрики на{' '}
        <Text variant="code-inline-2" as="span">
          POST /api/v1/ingest/metrics
        </Text>
        . Токен показывается только один раз при создании.
      </Text>
      {created && (
        <Card type="container" view="outlined">
          <div className="hs-col" style={{ gap: 'var(--g-spacing-2)', padding: 'var(--g-spacing-4)' }}>
            <Text variant="body-2">Токен для «{created.name}» — скопируйте сейчас, больше он показан не будет:</Text>
            <div className="hs-token-box">
              <Text variant="code-inline-2" className="hs-break">
                {created.token}
              </Text>
              <ClipboardButton text={created.token} />
            </div>
          </div>
        </Card>
      )}
      <form onSubmit={onSubmit} className="hs-row hs-row-end">
        <TextInput
          label="Название источника"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="ci-collector"
        />
        <Button type="submit" view="action" disabled={busy} loading={busy}>
          Создать источник
        </Button>
      </form>
      <Table data={sources.data?.sources ?? []} columns={columns} getRowId={(s) => s.id} />
    </div>
  )
}

function RunsTab({ id, trackKey }: { id: string; trackKey: string }) {
  const queryClient = useQueryClient()
  const runs = useQuery({
    queryKey: ['runs', id, trackKey],
    queryFn: () => api.get<{ runs: ScoreRun[] }>(`/hackathons/${id}/score-runs${trackKey ? `?track=${trackKey}` : ''}`),
    enabled: !!trackKey,
  })
  const [busy, setBusy] = useState(false)

  async function recalc() {
    setBusy(true)
    try {
      await api.post(`/hackathons/${id}/scoring/recalculate${trackKey ? `?track=${trackKey}` : ''}`)
      void queryClient.invalidateQueries({ queryKey: ['runs', id, trackKey] })
    } catch (err) {
      toaster.add({ name: 'toast-14', title: 'Не удалось пересчитать', content: err instanceof Error ? err.message : undefined, theme: 'danger' })
    } finally {
      setBusy(false)
    }
  }

  const columns: TableColumnConfig<ScoreRun>[] = [
    {
      id: 'started_at',
      name: 'Запуск',
      template: (r) => <Text variant="body-2">{new Date(r.started_at).toLocaleString()}</Text>,
    },
    { id: 'mode', name: 'Режим', template: (r) => <Text variant="body-2">{ru(runModeRu, r.mode)}</Text> },
    {
      id: 'status',
      name: 'Статус',
      template: (r) => (
        <Label size="s" theme={r.status === 'completed' ? 'success' : r.status === 'error' ? 'danger' : 'utility'}>
          {ru(runStatusRu, r.status)}
        </Label>
      ),
    },
    { id: 'version', name: 'Версия', template: (r) => <Text variant="body-2">v{r.version}</Text> },
    { id: 'teams_scored', name: 'Команды', align: 'end' },
    {
      id: 'config_hash',
      name: 'Хеш конфигурации',
      template: (r) => <Text variant="code-inline-2">{r.config_hash.slice(0, 12)}…</Text>,
    },
  ]

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)' }}>
      <Button view="action" onClick={() => void recalc()} disabled={busy} loading={busy}>
        Пересчитать сейчас
      </Button>
      <Table data={runs.data?.runs ?? []} columns={columns} getRowId={(r) => r.id} />
    </div>
  )
}

function AuditTab({ id }: { id: string }) {
  const audit = useQuery({
    queryKey: ['audit', id],
    queryFn: () => api.get<{ events: AuditEvent[] }>(`/hackathons/${id}/audit?limit=100`),
  })
  const columns: TableColumnConfig<AuditEvent>[] = [
    {
      id: 'created_at',
      name: 'Время',
      template: (e) => <Text variant="body-2">{new Date(e.created_at).toLocaleString()}</Text>,
    },
    { id: 'action', name: 'Действие', template: (e) => <Text variant="code-inline-2">{e.action}</Text> },
    { id: 'actor_name', name: 'Автор', template: (e) => <Text variant="body-2">{e.actor_name ?? 'система'}</Text> },
    { id: 'actor_role', name: 'Роль', template: (e) => <Text variant="body-2">{ru(roleRu, e.actor_role)}</Text> },
    { id: 'reason', name: 'Причина', template: (e) => <Text variant="body-2">{e.reason ?? ''}</Text> },
  ]
  return (
    <div style={{ paddingTop: 'var(--g-spacing-3)' }}>
      <Table data={audit.data?.events ?? []} columns={columns} getRowId={(e) => e.id} />
    </div>
  )
}

function FinalizeTab({ id }: { id: string }) {
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<string | null>(null)

  async function finalize() {
    setBusy(true)
    try {
      const res = await api.post<{ teams_scored: number; teams_eligible: number }>(
        `/hackathons/${id}/finalize`,
      )
      setResult(`Допущено: ${res.teams_eligible} из ${res.teams_scored}`)
      void queryClient.invalidateQueries()
    } catch (err) {
      setResult(err instanceof Error ? err.message : 'Не удалось финализировать')
    } finally {
      setBusy(false)
      setConfirming(false)
    }
  }

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-3)', maxWidth: 520 }}>
      <Text variant="body-2" color="secondary">
        При финализации баллы считаются по полному набору метрик (включая приватные),
        финальный лидерборд замораживается и становится неизменяемым, а хакатон блокируется.
      </Text>
      <div>
        <Button view="outlined-danger" onClick={() => setConfirming(true)}>
          Финализировать хакатон
        </Button>
      </div>
      {result && <Text variant="body-2">{result}</Text>}

      <Modal open={confirming} onClose={() => setConfirming(false)}>
        <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', padding: 'var(--g-spacing-5)', minWidth: 380 }}>
          <Text variant="subheader-1">Вы уверены? Это действие необратимо.</Text>
          <div className="hs-row" style={{ justifyContent: 'flex-end' }}>
            <Button view="flat-secondary" onClick={() => setConfirming(false)}>
              Отмена
            </Button>
            <Button view="outlined-danger" onClick={() => void finalize()} loading={busy}>
              Да, финализировать
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
