import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Button, Card, Label, Select, Text, TextArea, TextInput } from '@gravity-ui/uikit'
import { api, me, ApiError } from '~/lib/api'
import { defaultTrackKey, useTracks } from '~/lib/tracks'
import { roleOf, useMyRoles } from '~/lib/roles'
import { RoleBadge } from '~/components/RoleBadge'
import type { Hackathon, Submission, Team } from '~/lib/types'

export const Route = createFileRoute('/team/$slug')({
  component: TeamAreaPage,
})

function TeamAreaPage() {
  const { slug } = Route.useParams()
  const hackathon = useQuery({
    queryKey: ['hackathon', slug],
    queryFn: () => api.get<Hackathon>(`/hackathons/${slug}`),
  })
  const user = useQuery({ queryKey: ['me'], queryFn: () => me(), retry: false })
  const id = hackathon.data?.id
  const { roles } = useMyRoles()
  const role = roleOf(roles, id)

  const myTeam = useQuery({
    queryKey: ['my-team', id],
    queryFn: () => api.get<Team>(`/hackathons/${id}/my-team`),
    // Кабинет команды имеет смысл только для тех, кто может в нём состоять:
    // участник без команды и залогиненный зритель (создаст команду ниже).
    // Организатору и судье запрашивать нечего — им сразу показываем объяснение.
    enabled: !!id && user.isSuccess && role !== 'organizer' && role !== 'judge',
    retry: false,
  })

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
  if (user.isError) {
    return (
      <div className="hs-col hs-center" style={{ alignItems: 'center', padding: 'var(--g-spacing-6)', gap: 'var(--g-spacing-3)' }}>
        <Text variant="body-1" color="secondary">
          Войдите, чтобы управлять командой.
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

  // У организатора команды нет по определению: создание команды сделало бы его
  // участником собственного хакатона. Показываем объяснение вместо формы.
  if (role === 'organizer') {
    return (
      <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', maxWidth: 720 }}>
        <Text variant="header-1" as="h1">
          Кабинет команды: {hackathon.data.name}
        </Text>
        <RoleBadge role={role} />
        <Card type="container" view="outlined">
          <div className="hs-col" style={{ gap: 'var(--g-spacing-3)', padding: 'var(--g-spacing-4)' }}>
            <Text variant="subheader-1">У организатора нет команды</Text>
            <Text variant="body-2" color="secondary">
              Вы управляете этим хакатоном, а не участвуете в нём. Проекты команд
              смотрите на их публичных страницах через лидерборд — этот кабинет
              только для участников.
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

  // Судья не может одновременно судить и участвовать в одном хакатоне —
  // это конфликт интересов, поэтому форму создания команды не показываем.
  if (role === 'judge') {
    return (
      <div className="hs-col" style={{ gap: 'var(--g-spacing-4)', maxWidth: 720 }}>
        <Text variant="header-1" as="h1">
          Кабинет команды: {hackathon.data.name}
        </Text>
        <RoleBadge role={role} />
        <Card type="container" view="outlined">
          <div className="hs-col" style={{ gap: 'var(--g-spacing-3)', padding: 'var(--g-spacing-4)' }}>
            <Text variant="subheader-1">Судьи не участвуют командами</Text>
            <Text variant="body-2" color="secondary">
              Вы судите этот хакатон: создавать команду и редактировать проекты здесь
              нельзя. Ваше рабочее место — панель судьи.
            </Text>
            <div className="hs-row" style={{ gap: 'var(--g-spacing-4)' }}>
              <Link to="/judge/$slug" params={{ slug }} className="hs-nav-link">
                <Button view="action">Панель судьи</Button>
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

  if (myTeam.isLoading) {
    return (
      <Text variant="body-1" color="secondary" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Загрузка команды…
      </Text>
    )
  }

  // Неожиданная ошибка (не 404 «нет команды»): не притворяемся, что команды нет,
  // а честно показываем сбой — иначе форма создания сбивает с толку.
  if (myTeam.isError && (myTeam.error as ApiError)?.status !== 404 &&
      (myTeam.error as ApiError)?.code !== 'no_team') {
    return (
      <div className="hs-col hs-center" style={{ alignItems: 'center', padding: 'var(--g-spacing-6)', gap: 'var(--g-spacing-3)' }}>
        <Text variant="body-1" color="danger">
          Не удалось загрузить команду: {(myTeam.error as Error)?.message ?? 'ошибка сети'}
        </Text>
        <Link to="/h/$slug" params={{ slug }} className="hs-nav-link">
          <Text variant="body-2" color="link">
            Назад к лидерборду
          </Text>
        </Link>
      </div>
    )
  }

  return (
    <div className="hs-col" style={{ gap: 'var(--g-spacing-5)', maxWidth: 720 }}>
      <Text variant="header-1" as="h1">
        Кабинет команды: {hackathon.data.name}
      </Text>
      <RoleBadge role={role} />
      {myTeam.data ? (
        <SubmissionEditor hackathonId={id} team={myTeam.data} />
      ) : (
        <>
          <Text variant="body-2" color="secondary">
            У вас пока нет команды в этом хакатоне. Создайте её — вы станете её
            капитаном и участником хакатона. Одна команда на человека.
          </Text>
          <CreateTeamForm hackathonId={id} />
        </>
      )}
    </div>
  )
}

function CreateTeamForm({ hackathonId }: { hackathonId: string }) {
  const queryClient = useQueryClient()
  const tracksQuery = useTracks(hackathonId)
  const tracks = tracksQuery.data?.tracks ?? []
  const [name, setName] = useState('')
  const [track, setTrack] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const effectiveTrack = track || defaultTrackKey(tracks)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) {
      setError('Нужно указать название команды')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await api.post(`/hackathons/${hackathonId}/teams`, {
        name: name.trim(),
        track: effectiveTrack || undefined,
      })
      await queryClient.invalidateQueries({ queryKey: ['my-team', hackathonId] })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'не удалось создать команду')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card type="container" view="outlined">
      <form onSubmit={onSubmit} className="hs-col" style={{ gap: 'var(--g-spacing-4)', padding: 'var(--g-spacing-4)' }}>
        <Text variant="subheader-1">Создание команды</Text>
        <TextInput label="Название команды" value={name} onChange={(e) => setName(e.target.value)} />
        {tracks.length > 1 && (
          <Select
            label="Трек"
            value={effectiveTrack ? [effectiveTrack] : []}
            onUpdate={(v) => setTrack(Array.isArray(v) ? (v[0] ?? '') : (v ?? ''))}
            options={tracks.map((t) => ({ value: t.key, content: t.name }))}
          />
        )}
        {error && (
          <Text variant="body-2" color="danger">
            {error}
          </Text>
        )}
        <Button type="submit" view="action" width="max" loading={busy}>
          Создать команду
        </Button>
      </form>
    </Card>
  )
}

function SubmissionEditor({ hackathonId, team }: { hackathonId: string; team: Team }) {
  const queryClient = useQueryClient()
  const submission = useQuery({
    queryKey: ['submission', hackathonId, team.id],
    queryFn: () => api.get<Submission>(`/hackathons/${hackathonId}/submissions/${team.id}`),
    retry: false,
  })

  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [track, setTrack] = useState(team.track_name ?? '')
  const [repoUrl, setRepoUrl] = useState('')
  const [demoUrl, setDemoUrl] = useState('')
  const [appUrl, setAppUrl] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)
  const initialized = useRef(false)

  useEffect(() => {
    if (!submission.data || initialized.current) return
    initialized.current = true
    setTitle(submission.data.title ?? '')
    setDescription(submission.data.description ?? '')
    setTrack(submission.data.track || team.track_name || '')
    setRepoUrl(submission.data.repo_url ?? '')
    setDemoUrl(submission.data.demo_url ?? '')
    setAppUrl(submission.data.app_url ?? '')
  }, [submission.data, team.track_name])

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!title.trim()) {
      setError('Нужно указать название проекта')
      return
    }
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      await api.put(`/hackathons/${hackathonId}/submissions/${team.id}`, {
        title: title.trim(),
        description,
        track: track.trim(),
        repo_url: repoUrl.trim(),
        demo_url: demoUrl.trim(),
        app_url: appUrl.trim(),
      })
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['submission', hackathonId, team.id] })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'не удалось сохранить проект')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card type="container" view="outlined">
      <form onSubmit={onSubmit} className="hs-col" style={{ gap: 'var(--g-spacing-4)', padding: 'var(--g-spacing-4)' }}>
        <div className="hs-justify-between">
          <div className="hs-col" style={{ gap: 0 }}>
            <Text variant="subheader-1">{team.name}</Text>
            <Text variant="body-2" color="secondary">
              Участников: {team.member_count}
              {team.track_name ? ` · ${team.track_name}` : ''}
              {submission.data && ` · версия ${submission.data.version}`}
            </Text>
          </div>
          {saved && (
            <Label theme="success" size="s">
              Сохранено
            </Label>
          )}
        </div>
        <TextInput label="Название проекта" value={title} onChange={(e) => setTitle(e.target.value)} />
        <div>
          <Text variant="body-2" color="secondary">
            Описание
          </Text>
          <TextArea value={description} onChange={(e) => setDescription(e.target.value)} minRows={4} />
        </div>
        <TextInput label="Трек" value={track} onChange={(e) => setTrack(e.target.value)} />
        <TextInput label="URL репозитория" value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} />
        <TextInput label="URL демо" value={demoUrl} onChange={(e) => setDemoUrl(e.target.value)} />
        <TextInput label="URL приложения" value={appUrl} onChange={(e) => setAppUrl(e.target.value)} />
        {error && (
          <Text variant="body-2" color="danger">
            {error}
          </Text>
        )}
        <Button type="submit" view="action" width="max" loading={busy}>
          Сохранить проект
        </Button>
        <Text variant="caption-2" color="hint">
          Каждое сохранение создаёт новую неизменяемую версию. После дедлайна изменения заблокированы.
        </Text>
      </form>
    </Card>
  )
}
