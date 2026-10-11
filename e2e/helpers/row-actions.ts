import { expect, type Locator, type Page } from '@playwright/test'

/** A single action is inline; multiple actions share the compact row popover. */
export async function clickRowAction(page: Page, row: Locator, name: string) {
  await expect(row).toBeVisible()
  const direct = row.getByRole('button', { name, exact: true })
  if (await direct.isVisible()) return direct.click()
  await row.getByRole('button', { name: /^操作：/ }).click()
  await page.locator('.row-actions-panel:visible').getByRole('button', { name, exact: true }).click()
}
