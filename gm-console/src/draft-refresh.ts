/** Refresh untouched values while preserving edits to the current object. */
export function refreshDraftValue<T>(draft: T, previous: T, next: T, changedOwner = false): T {
  return changedOwner || Object.is(draft, previous) ? next : draft;
}
