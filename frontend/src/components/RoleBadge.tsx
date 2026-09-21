import { Label, Text } from '@gravity-ui/uikit'
import type { HackathonRole } from '~/lib/roles'
import { roleRu, ru } from '~/lib/labels'

// Бейдж «ваша роль в этом хакатоне». Для anonymous/public ничего не рисует:
// у них нет особой роли, и подсказывать нечего.
export function RoleBadge({ role }: { role: HackathonRole | undefined }) {
  if (!role) return null
  const theme = role === 'organizer' ? 'info' : role === 'judge' ? 'warning' : 'success'
  return (
    <Label theme={theme} size="m">
      ваша роль: {ru(roleRu, role)}
    </Label>
  )
}

// Короткое объяснение, куда пользователю с такой ролью надо, а куда — не надо.
export function RoleHint({ role }: { role: HackathonRole | undefined }) {
  if (role === 'organizer') {
    return (
      <Text variant="body-2" color="secondary">
        Вы — организатор этого хакатона: управляйте им из панели организатора. Команды
        у организатора нет, судейских оценок он не ставит.
      </Text>
    )
  }
  if (role === 'judge') {
    return (
      <Text variant="body-2" color="secondary">
        Вы — судья этого хакатона: оценивайте назначенные команды в панели судьи.
        Лидерборд откроется после финализации, а участвовать командой здесь нельзя.
      </Text>
    )
  }
  if (role === 'team_member') {
    return (
      <Text variant="body-2" color="secondary">
        Вы — участник этого хакатона: проект редактируется в кабинете команды.
      </Text>
    )
  }
  return null
}
