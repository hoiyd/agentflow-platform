import { expect, test } from "@playwright/test";

// Regression risks: touching controls, vertically stretched labels, and a
// max-width toolbar anchored to the left of the centered composer.
test("Multi-agent and Bounded loop keep Run settings spaced and aligned", async ({ page }, info) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const geometry = [];
  for (const mode of ["Multi-agent", "Bounded loop"]) {
    await page.getByRole("region", { name: "Chat mode", exact: true })
      .getByRole("button", { name: new RegExp(mode) }).click();
    const toolbar = page.locator(".composer-run-options");
    const planning = toolbar.getByRole("checkbox", { name: "Read-only planning", exact: true });
    for (const width of [1280, 1920]) {
      await page.setViewportSize({ width, height: 900 });
      const controls = [toolbar.getByRole("button", { name: "Knowledge scope for new runs" }),
        toolbar.locator(".planning-toggle"), toolbar.getByRole("button", { name: "Verification Off", exact: true })];
      const bounds = [];
      for (const control of controls) {
        await expect(control).toBeVisible();
        const rect = (await control.boundingBox())!;
        expect(await control.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
        if (bounds.length) {
          const previous = bounds.at(-1)!;
          expect(rect.x - previous.x - previous.width).toBeGreaterThanOrEqual(12);
          expect(Math.abs(rect.y + rect.height / 2 - previous.y - previous.height / 2)).toBeLessThanOrEqual(1);
        }
        bounds.push(rect);
      }
      const row = (await toolbar.boundingBox())!;
      const composer = (await page.locator(".composer-inner").boundingBox())!;
      expect(Math.abs(row.x - composer.x)).toBeLessThanOrEqual(1);
      expect(Math.abs(row.width - composer.width)).toBeLessThanOrEqual(1);
      expect(Math.abs(bounds.at(-1)!.x + bounds.at(-1)!.width - row.x - row.width)).toBeLessThanOrEqual(1);
      expect(row.height).toBeLessThanOrEqual(40);
      await planning.check();
      await expect(planning).toBeChecked();
      await planning.uncheck();
      geometry.push({ mode, viewport: { width, height: 900 }, row, composer, controls: bounds });
      const image = info.outputPath(`${mode.toLowerCase().replaceAll(" ", "-")}-${width}.png`);
      await page.locator(".composer").screenshot({ path: image });
      await info.attach(`${mode}-${width}`, { path: image, contentType: "image/png" });
    }
  }
  await page.getByRole("region", { name: "Chat mode", exact: true })
    .getByRole("button", { name: /Single agent/ }).click();
  await expect(page.locator(".composer-run-options")).toHaveCount(0);
  await expect(page.getByRole("group", { name: "Agent and run settings" })).toBeVisible();
  await info.attach("composer-layout.json", { body: JSON.stringify({
    geometry, limitations: ["desktop layout only", "no model requests", "disposable Postgres"],
  }, null, 2), contentType: "application/json" });
});
