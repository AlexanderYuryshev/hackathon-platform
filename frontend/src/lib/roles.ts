import { useQuery } from '@tanstack/react-query'
import { api, me } from '~/lib/api'

export type HackathonRole = 'organizer' | 'judge' | 'team_member'

// Модель ролей (роль — всегда в контексте одного хакатона, глобальных ролей нет):
// - anonymous: не залогинен. Видит публичное: список хакатонов, лидерборд,
//   страницу команды. Точка входа для участия — /team/$slug (там — приглашение войти).
// - public (залогинен, но без роли в этом хакатоне — в карте ролей ключа нет):
//   то же, что anonymous, плюс может создать команду (/team/$slug) и тем самым
//   стать team_member, а также создать свой хакатон (/hackathons/new).
// - team_member: участник с командой (одна команда на хакатон). Может смотреть
//   лидерборд и редактировать сабмишен СВОЕЙ команды в /team/$slug.
//   Не ходит в /judge/* и /app/* этого хакатона.
// - judge: назначается организатором. Работает только в /judge/$slug
//   (свои назначения, оценки, самоотвод). Лидерборд ему закрыт слепотой
//   (judge_blind) до финализации; команды в этом хакатоне создавать нельзя
//   (конфликт интересов). Панель организатора не предлагается.
// - organizer: создатель хакатона. Работает только в консоли /app/hackathons/$id
//   и публичном лидерборде. Команды у него нет по определению — /team/$slug
//   ему не предлагается (иначе он стал бы участником собственного хакатона),
//   судейских назначений нет — /judge/* не предлагается.
// При пересечении ролей в одном хакатоне бэкенд отдаёт старшую
// (organizer > judge > team_member, см. /my-roles и rbac.Resolver).
//
// Роли текущего пользователя по хакатонам (id -> роль, только непубличные).
// Отсутствие ключа = публичная/анонимная роль (см. isAuthenticated).
export function useMyRoles() {
  const user = useQuery({ queryKey: ['me'], queryFn: () => me(), retry: false })
  const roles = useQuery({
    queryKey: ['my-roles'],
    queryFn: () => api.get<{ roles: Record<string, HackathonRole> }>('/my-roles'),
    enabled: user.isSuccess,
    retry: false,
    staleTime: 60_000,
  })
  return {
    isAuthenticated: user.isSuccess,
    isLoading: user.isLoading || (user.isSuccess && roles.isLoading),
    roles: roles.data?.roles ?? {},
  }
}

// Роль текущего пользователя в конкретном хакатоне.
// undefined = нет особой роли (anonymous либо public — различайте по isAuthenticated).
export function roleOf(roles: Record<string, HackathonRole>, hackathonId: string | undefined): HackathonRole | undefined {
  if (!hackathonId) return undefined
  return roles[hackathonId]
}
