import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Button, Text, TextArea, TextInput } from '@gravity-ui/uikit'
import { api, me } from '~/lib/api'
import type { Hackathon } from '~/lib/types'

export const Route = createFileRoute('/hackathons/new')({
  component: NewHackathonPage,
})

const slugRe = /^[a-z0-9][a-z0-9-]{2,62}[a-z0-9]$/

function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60)
}

function trackKey(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 64)
}

function toRFC3339(v: string): string | undefined {
  return v ? new Date(v).toISOString() : undefined
}

function NewHackathonPage() {
  const navigate = useNavigate()
  const user = useQuery({ queryKey: ['me'], queryFn: () => me(), retry: false })

  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [slugTouched, setSlugTouched] = useState(false)
  const [description, setDescription] = useState('')
  const [startsAt, setStartsAt] = useState('')
  const [endsAt, setEndsAt] = useState('')
  const [judgingEndsAt, setJudgingEndsAt] = useState('')
  const [tracks, setTracks] = useState([{ name: 'Общий зачёт', key: 'general', minJudges: '3' }])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  if (user.isLoading) {
    return (
      <Text variant="body-2" color="secondary" className="hs-center" style={{ padding: 'var(--g-spacing-6)' }}>
        Загрузка…
      </Text>
    )
  }
  if (user.isError) {
    return (
      <div className="hs-col hs-center" style={{ alignItems: 'center', padding: 'var(--g-spacing-6)', gap: 'var(--g-spacing-3)' }}>
        <Text variant="body-1" color="secondary">
          Войдите как организатор, чтобы создать хакатон.
        </Text>
        <Link to="/login" className="hs-nav-link">
          <Text variant="body-2" color="link">
            Перейти ко входу
          </Text>
        </Link>
      </div>
    )
  }

  const effectiveSlug = slugTouched ? slug : slugify(name)
  const slugInvalid = effectiveSlug.length > 0 && !slugRe.test(effectiveSlug)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    if (!slugRe.test(effectiveSlug)) {
      setError('Слаг: от 4 до 64 символов — строчные латинские буквы, цифры и дефисы')
      return
    }
    const trackSpecs = tracks.map((t) => ({
      key: t.key.trim() || trackKey(t.name),
      name: t.name.trim(),
      min_judges_per_team: Number(t.minJudges) || 3,
    }))
    if (trackSpecs.some((t) => !t.name)) {
      setError('У каждого трека должно быть название')
      return
    }
    if (trackSpecs.some((t) => !t.key)) {
      setError('У каждого трека должен быть ключ (латиница, цифры, дефисы)')
      return
    }
    if (new Set(trackSpecs.map((t) => t.key)).size !== trackSpecs.length) {
      setError('Ключи треков должны различаться')
      return
    }
    if (trackSpecs.some((t) => t.min_judges_per_team < 1)) {
      setError('Минимум судей — не меньше 1')
      return
    }
    setBusy(true)
    try {
      const hk = await api.post<Hackathon>('/hackathons', {
        slug: effectiveSlug,
        name: name.trim(),
        description: description.trim(),
        starts_at: toRFC3339(startsAt),
        ends_at: toRFC3339(endsAt),
        judging_ends_at: toRFC3339(judgingEndsAt),
        tracks: trackSpecs,
      })
      void navigate({ to: '/app/hackathons/$id', params: { id: hk.id } })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'не удалось создать хакатон')
    } finally {
      setBusy(false)
    }
  }

  function updateTrack(i: number, patch: Partial<(typeof tracks)[number]>) {
    setTracks((prev) => prev.map((t, j) => (j === i ? { ...t, ...patch } : t)))
  }

  function removeTrack(i: number) {
    setTracks((prev) => (prev.length > 1 ? prev.filter((_, j) => j !== i) : prev))
  }

  return (
    <div className="hs-col" style={{ maxWidth: 720, margin: '0 auto', gap: 'var(--g-spacing-4)', paddingTop: 'var(--g-spacing-4)' }}>
      <Text variant="header-1" as="h1">
        Новый хакатон
      </Text>
      <Text variant="body-2" color="secondary">
        Хакатон создаётся как черновик: сначала настройте метрики и критерии,
        затем запустите его из панели организатора.
      </Text>
      <form onSubmit={onSubmit} className="hs-col" style={{ gap: 'var(--g-spacing-4)' }}>
        <TextInput
          label="Название"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Демо-хакатон 2027"
          controlProps={{ required: true, id: 'hk-name' }}
        />
        <TextInput
          label="Слаг (публичный URL: /h/<slug>)"
          value={effectiveSlug}
          onChange={(e) => {
            setSlugTouched(true)
            setSlug(e.target.value)
          }}
          placeholder="demo-hack-2027"
          errorMessage={slugInvalid ? 'Только строчные латинские буквы, цифры и дефисы (4–64 символа)' : undefined}
          controlProps={{ id: 'hk-slug' }}
        />
        <div>
          <Text variant="body-2" color="secondary">
            Описание
          </Text>
          <TextArea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            minRows={2}
            controlProps={{ id: 'hk-desc' }}
          />
        </div>
        <div className="hs-grid-form">
          <TextInput
            label="Дата начала"
            value={startsAt}
            onChange={(e) => setStartsAt(e.target.value)}
            controlProps={{ type: 'datetime-local', id: 'hk-start' }}
          />
          <TextInput
            label="Дедлайн сдачи проектов"
            value={endsAt}
            onChange={(e) => setEndsAt(e.target.value)}
            controlProps={{ type: 'datetime-local', id: 'hk-end' }}
          />
          <TextInput
            label="Окончание судейства"
            value={judgingEndsAt}
            onChange={(e) => setJudgingEndsAt(e.target.value)}
            controlProps={{ type: 'datetime-local', id: 'hk-judging' }}
          />
        </div>
        <div className="hs-col" style={{ gap: 'var(--g-spacing-2)' }}>
          <Text variant="body-2" color="secondary">
            Треки — отдельные задачи со своими метриками и лидербордами. Один трек
            по умолчанию уже добавлен.
          </Text>
          {tracks.map((t, i) => (
            <div key={i} className="hs-row hs-row-end">
              <TextInput
                label={i === 0 ? 'Название трека' : ''}
                value={t.name}
                onChange={(e) => updateTrack(i, { name: e.target.value })}
                placeholder="Искусственный интеллект"
                controlProps={{ required: true }}
                style={{ flex: 2 }}
              />
              <TextInput
                label={i === 0 ? 'Ключ' : ''}
                value={t.key}
                onChange={(e) => updateTrack(i, { key: e.target.value })}
                placeholder="ai"
                style={{ flex: 1, minWidth: 120 }}
              />
              <TextInput
                label={i === 0 ? 'Судей (мин.)' : ''}
                value={t.minJudges}
                onChange={(e) => updateTrack(i, { minJudges: e.target.value })}
                controlProps={{ type: 'number', min: 1 }}
                style={{ maxWidth: 130 }}
              />
              <Button
                view="flat"
                disabled={tracks.length <= 1}
                onClick={() => removeTrack(i)}
                title="Убрать трек"
              >
                ✕
              </Button>
            </div>
          ))}
          <div>
            <Button view="outlined" onClick={() => setTracks((prev) => [...prev, { name: '', key: '', minJudges: '3' }])}>
              Добавить трек
            </Button>
          </div>
        </div>
        {error && (
          <Text variant="body-2" color="danger">
            {error}
          </Text>
        )}
        <div className="hs-row">
          <Button type="submit" view="action" disabled={busy || !name.trim()} loading={busy}>
            Создать хакатон
          </Button>
          <Link to="/" className="hs-nav-link">
            <Button view="flat-secondary">Отмена</Button>
          </Link>
        </div>
      </form>
    </div>
  )
}
