import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { Button, PasswordInput, Text, TextInput } from '@gravity-ui/uikit'
import { login } from '~/lib/api'

export const Route = createFileRoute('/login')({
  component: LoginPage,
})

function LoginPage() {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(email, password)
      // Roles are hackathon-scoped on the backend, so there is no global
      // "judge/organizer" destination: send the user to the hackathon list.
      void navigate({ to: '/' })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'не удалось войти')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="hs-col" style={{ maxWidth: 360, margin: '0 auto', paddingTop: 'var(--g-spacing-6)' }}>
      <Text variant="header-1" as="h1">
        Вход
      </Text>
      <form onSubmit={onSubmit} className="hs-col" style={{ gap: 'var(--g-spacing-4)' }}>
        <TextInput
          label="Email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          controlProps={{ type: 'email', required: true, id: 'email' }}
        />
        <PasswordInput
          label="Пароль"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          controlProps={{ required: true, id: 'password' }}
        />
        {error && (
          <Text variant="body-2" color="danger">
            {error}
          </Text>
        )}
        <Button type="submit" view="action" width="max" size="l" loading={busy}>
          Войти
        </Button>
      </form>
      <Text variant="caption-2" color="hint" style={{ marginTop: 'var(--g-spacing-4)' }}>
        Демо-аккаунты: organizer@demo.io, judge1@demo.io … judge8@demo.io,
        member001@demo.io … (пароль: demo1234)
      </Text>
    </div>
  )
}
