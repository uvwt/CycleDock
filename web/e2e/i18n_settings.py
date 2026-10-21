"""Frontend V2 English settings navigation and form regression against the isolated fixture.

python e2e/i18n_settings.py http://127.0.0.1:4188
"""
import sys
import unittest
from playwright.sync_api import sync_playwright

BASE = sys.argv.pop(1) if len(sys.argv) > 1 else "http://127.0.0.1:4188"


class SettingsEnglishTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.pw = sync_playwright().start()
        cls.browser = cls.pw.chromium.launch(headless=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.pw.stop()

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 1440, "height": 900})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.goto(BASE + "/settings/general")
        self.page.get_by_role("combobox", name="界面语言").click()
        self.page.get_by_role("option", name="English").click()
        self.page.get_by_role("heading", name="Settings", exact=True).first.wait_for()

    def tearDown(self):
        self.assertEqual(self.errors, [])
        self.page.close()

    def test_navigation_and_preference_persist_after_refresh(self):
        p = self.page
        p.get_by_role("combobox", name="Language").wait_for()
        p.get_by_role("button", name="Back to workspace").wait_for()
        p.get_by_role("button", name="Model providers").wait_for()
        p.reload()
        p.get_by_role("combobox", name="Language").wait_for()
        self.assertEqual(p.evaluate("document.documentElement.lang"), "en")
        p.get_by_role("button", name="Agent settings").click()
        p.wait_for_url("**/settings/agent")
        p.get_by_text("Enable built-in Agent").wait_for()
        p.get_by_role("button", name="Save Agent settings").wait_for()
        p.get_by_role("button", name="Test selected model").wait_for()

    def test_all_settings_sections_have_english_labels(self):
        p = self.page
        views = [
            ("Account", "/settings/account", "Current workspace"),
            ("Model providers", "/settings/models", "Authorize with Grok"),
            ("NexusDock", "/settings/nexusdock", "NexusDock device access"),
            ("MCP services", "/settings/mcp", "Saved MCP connections"),
            ("Project settings", "/settings/project", "Save project"),
        ]
        for button, path, phrase in views:
            p.goto(BASE + path)
            p.get_by_text(phrase, exact=False).first.wait_for()
            if button != "NexusDock":
                p.get_by_role("button", name=button, exact=True).wait_for()

    def test_main_workspace_discussion_board_and_card_controls(self):
        p = self.page
        p.goto(BASE + "/projects/demo-project/discussions")
        p.get_by_role("heading", name="What would you like to move forward today?").wait_for()
        p.locator('aside nav[aria-label="Work mode"]').get_by_role("button", name="Work").click()
        p.wait_for_url("**/projects/demo-project/work")
        p.get_by_role("heading", name="Todo board").wait_for()
        p.get_by_role("button", name="New to-do").click()
        p.get_by_role("dialog").get_by_role("heading", name="New to-do").wait_for()
        p.keyboard.press("Escape")
        p.goto(BASE + "/projects/demo-project/work/todo-1")
        p.get_by_role("button", name="Card information").click()
        p.get_by_text("Source discussion", exact=True).wait_for()
        p.keyboard.press("Escape")
        p.goto(BASE + "/projects/demo-project/work/ongoing-1")
        p.get_by_role("button", name="Card information").click()
        p.get_by_role("heading", name="Schedules").wait_for()
        p.get_by_role("button", name="Add schedule").click()
        p.get_by_role("combobox", name="Schedule frequency").click()
        p.get_by_role("option", name="Weekly").click()
        p.get_by_role("combobox", name="Weekday").wait_for()

    def test_mobile_navigation_and_english_form_edit(self):
        p = self.page
        p.set_viewport_size({"width": 390, "height": 844})
        p.reload()
        p.get_by_role("button", name="Settings categories").click()
        p.get_by_role("dialog").get_by_role("button", name="MCP services").click()
        p.wait_for_url("**/settings/mcp")
        p.get_by_role("button", name="Add connection").wait_for()
        p.get_by_role("button", name="Test connection").wait_for()
        metrics = p.evaluate("() => [document.documentElement.scrollWidth, innerWidth]")
        self.assertLessEqual(metrics[0], metrics[1])


if __name__ == "__main__":
    unittest.main(verbosity=2)
