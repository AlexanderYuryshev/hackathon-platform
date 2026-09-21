// Русские подписи для значений перечислений API.
// Ключи API (статусы, режимы, роли) не меняются — переводится только отображение.

export function ru(map: Record<string, string>, key: string): string {
  return map[key] ?? key
}

export const hackathonStatusRu: Record<string, string> = {
  draft: 'черновик',
  running: 'идёт',
  judging: 'судейство',
  finalized: 'завершён',
}

export const scoreStatusRu: Record<string, string> = {
  provisional: 'Предварительный',
  complete: 'Полный',
}

export const metricStatusRu: Record<string, string> = {
  fresh: 'свежее',
  stale: 'устарело',
  missing: 'нет данных',
  provisional: 'предварительно',
}

export const roleRu: Record<string, string> = {
  organizer: 'организатор',
  judge: 'судья',
  team_member: 'участник',
  public: 'пользователь',
}

export const runModeRu: Record<string, string> = {
  live: 'текущий',
  final: 'финальный',
}

export const runStatusRu: Record<string, string> = {
  running: 'выполняется',
  completed: 'завершён',
  error: 'ошибка',
}

export const assignmentStatusRu: Record<string, string> = {
  assigned: 'назначен',
  recused: 'самоотвод',
}
