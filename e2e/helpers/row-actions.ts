import { expect, type Locator, type Page } from '@playwright/test'

/** A single action is inline; multiple actions share the compact row popover. */
export async function clickRowAction(page: Page, row: Locator, name: string) {
  await expect(row).toBeVisible()
  const direct = row.getByRole('button', { name, exact: true })
  if (await direct.isVisible()) return direct.click()
  await row.getByRole('button', { name: /^操作：/ }).click()
  const panel = page.locator('.row-actions-panel:visible').filter({ has: page.getByRole('button', { name, exact: true }) }).last()
  await expect(panel).toBeVisible()
  await expect(panel.getByRole('button', { name, exact: true })).toBeVisible()
  // Element Plus may rerender the teleported popover between the visibility
  // assertion and Playwright's stability check. Re-resolve the action after
  // the panel is open and force the native click to avoid a false timeout.
  await panel.getByRole('button', { name, exact: true }).evaluate((element) => (element as HTMLElement).click())
}
