// Shared test helpers. Import from here (not '@testing-library/react' directly) so every test gets the
// same render setup — today a thin pass-through, the single seam to add context providers (theme, router,
// project state) as the app grows, without touching every test.
import { render, type RenderOptions } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { type ReactElement } from 'react'

export function renderUI(ui: ReactElement, options?: RenderOptions) {
  return {
    user: userEvent.setup(),
    ...render(ui, options),
  }
}

export * from '@testing-library/react'
export { default as userEvent } from '@testing-library/user-event'
