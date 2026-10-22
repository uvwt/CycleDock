"""V3 shell theme and full-height navigation regression.

python e2e/shell_theme.py http://127.0.0.1:4188
"""
import re
import sys
import unittest
from playwright.sync_api import sync_playwright

BASE = sys.argv.pop(1) if len(sys.argv) > 1 else "http://127.0.0.1:4188"


def rgb_channels(value: str):
    match = re.search(r"rgba?\((\d+),\s*(\d+),\s*(\d+)", value)
    if not match:
        raise AssertionError(f"无法解析颜色: {value}")
    return tuple(int(part) for part in match.groups())


class ShellThemeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.pw = sync_playwright().start()
        cls.browser = cls.pw.chromium.launch(headless=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.pw.stop()

    def open_work(self, theme: str, viewport=None):
        page = self.browser.new_page(
            viewport=viewport or {"width": 1440, "height": 900}
        )
        page.add_init_script(
            f"localStorage.setItem('cycledock.theme', {theme!r})"
        )
        page.goto(BASE + "/projects/demo-project/work")
        page.get_by_role("heading", name="待办看板").wait_for()
        return page

    def test_light_sidebar_is_light_and_fills_viewport(self):
        page = self.open_work("light")
        metrics = page.evaluate(
            """() => {
                const sidebar = document.querySelector(".product-sidebar");
                const frame = document.querySelector(".v2-app-frame");
                const s = getComputedStyle(sidebar);
                return {
                    theme: document.documentElement.dataset.theme,
                    sidebarBg: s.backgroundColor,
                    sidebarHeight: sidebar.getBoundingClientRect().height,
                    frameHeight: frame.getBoundingClientRect().height,
                    viewportHeight: innerHeight,
                };
            }"""
        )
        self.assertEqual(metrics["theme"], "light")
        channels = rgb_channels(metrics["sidebarBg"])
        self.assertTrue(all(channel >= 180 for channel in channels), metrics)
        self.assertLessEqual(
            abs(metrics["sidebarHeight"] - metrics["viewportHeight"]), 2, metrics
        )
        self.assertLessEqual(
            abs(metrics["frameHeight"] - metrics["viewportHeight"]), 2, metrics
        )
        page.close()

    def test_dark_sidebar_is_dark_and_fills_viewport(self):
        page = self.open_work("dark")
        metrics = page.evaluate(
            """() => {
                const sidebar = document.querySelector(".product-sidebar");
                return {
                    theme: document.documentElement.dataset.theme,
                    sidebarBg: getComputedStyle(sidebar).backgroundColor,
                    sidebarHeight: sidebar.getBoundingClientRect().height,
                    viewportHeight: innerHeight,
                };
            }"""
        )
        self.assertEqual(metrics["theme"], "dark")
        channels = rgb_channels(metrics["sidebarBg"])
        self.assertTrue(all(channel <= 80 for channel in channels), metrics)
        self.assertLessEqual(
            abs(metrics["sidebarHeight"] - metrics["viewportHeight"]), 2, metrics
        )
        page.close()

    def test_mobile_bar_tracks_light_theme_without_root_overflow(self):
        page = self.open_work("light", {"width": 390, "height": 844})
        metrics = page.evaluate(
            """() => {
                const bar = document.querySelector(".product-mobile-bar");
                return {
                    barBg: getComputedStyle(bar).backgroundColor,
                    width: innerWidth,
                    scrollWidth: document.documentElement.scrollWidth,
                };
            }"""
        )
        channels = rgb_channels(metrics["barBg"])
        self.assertTrue(all(channel >= 180 for channel in channels), metrics)
        self.assertEqual(metrics["width"], 390)
        self.assertEqual(metrics["scrollWidth"], 390)
        page.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
