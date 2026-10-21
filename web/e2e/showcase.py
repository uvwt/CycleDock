"""Frontend V2 Design System / UI Showcase 真实浏览器验收。

运行：python e2e/showcase.py http://127.0.0.1:5175
使用本机 Chromium 的真实布局与 Radix 交互，不依赖后端 fixture。
"""
import sys
import unittest
from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:5175"
if len(sys.argv) > 1:
    sys.argv.pop(1)

class ShowcaseTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.driver = sync_playwright().start()
        cls.browser = cls.driver.chromium.launch(headless=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.driver.stop()

    def setUp(self):
        self.page = cls_page = self.browser.new_page(viewport={"width": 1440, "height": 900})
        self.errors = []
        cls_page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.goto(BASE + "/__ui")
        self.page.get_by_test_id("v2-showcase").wait_for()

    def tearDown(self):
        self.assertFalse(self.errors, "Page JS errors: " + repr(self.errors))
        self.page.close()

    def test_typography_and_controls_are_token_driven(self):
        selector = self.page.get_by_role("combobox", name="任务类型")
        self.assertEqual(selector.evaluate("(el) => getComputedStyle(el).fontSize"), "13px")
        self.assertEqual(selector.evaluate("(el) => el.getBoundingClientRect().height"), 36)
        self.assertEqual(self.page.get_by_role("button", name="保存修改", exact=True).evaluate("(el) => el.getBoundingClientRect().height"), 36)
        self.assertEqual(self.page.get_by_role("button", name="紧凑 32").evaluate("(el) => el.getBoundingClientRect().height"), 32)
        self.assertEqual(self.page.get_by_role("button", name="强调 40").evaluate("(el) => el.getBoundingClientRect().height"), 40)
        self.page.get_by_role("combobox", name="任务类型").click()
        self.page.get_by_role("option", name="待办 · Todo").click()
        self.assertIn("待办 · Todo", selector.inner_text())

    def test_dialog_popover_menu_sheet_focus_and_dismissal(self):
        self.page.get_by_role("button", name="新建项目 Dialog").click()
        self.page.get_by_role("dialog").get_by_role("heading", name="新建项目").wait_for()
        self.page.keyboard.press("Escape")
        self.assertEqual(self.page.get_by_role("dialog").count(), 0)
        self.page.get_by_role("button", name="详情 Popover").click()
        self.page.get_by_text("不使用固定右侧栏。").wait_for()
        self.page.keyboard.press("Escape")
        self.assertEqual(self.page.get_by_text("不使用固定右侧栏。").count(), 0)
        self.page.get_by_role("button", name="更多菜单").click()
        self.page.get_by_role("menuitem", name="切换 NexusDock").click()
        self.page.get_by_role("button", name="NexusDock", exact=True).wait_for()
        self.page.get_by_role("button", name="详情 Sheet").click()
        self.page.get_by_role("dialog").get_by_text("窄屏不占用主内容宽度。").wait_for()
        self.page.keyboard.press("Escape")
        self.assertEqual(self.page.get_by_role("dialog").count(), 0)

    def test_messages_are_independent_and_run_duration_stays_on_agent(self):
        self.assertEqual(self.page.locator('[data-message-role="human"]').count(), 2)
        self.assertEqual(self.page.locator('[data-message-role="agent"]').count(), 2)
        self.assertEqual(self.page.locator('[data-message-role="system"]').count(), 1)
        self.assertEqual(self.page.locator('[data-message-role="agent"] [data-run-duration]').count(), 2)
        self.assertEqual(self.page.locator('[data-message-role="human"] [data-run-duration]').count(), 0)

    def test_skeleton_is_delayed_and_structured(self):
        self.page.get_by_role("button", name="显示骨架").click()
        self.page.get_by_label("正在加载看板").wait_for()
        self.page.get_by_label("正在加载消息").first.wait_for()
        self.page.get_by_label("正在加载设置").wait_for()
        self.assertEqual(self.page.get_by_label("正在加载看板").locator(":scope > .v2-panel").count(), 4)

    def test_mobile_viewport_and_dismissible_navigation(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.page.reload()
        self.page.get_by_test_id("v2-showcase").wait_for()
        metrics = self.page.evaluate("() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth, body: document.body.scrollWidth, root: document.getElementById('root').scrollWidth })")
        self.assertLessEqual(metrics["document"], 390, metrics)
        self.assertLessEqual(metrics["body"], 390, metrics)
        self.assertLessEqual(metrics["root"], 390, metrics)
        self.page.get_by_role("button", name="打开导航").click()
        self.page.get_by_role("dialog").get_by_role("heading", name="导航").wait_for()
        self.page.keyboard.press("Escape")
        self.page.get_by_role("button", name="详情 Sheet").click()
        self.page.get_by_role("dialog").get_by_role("heading", name="任务信息").wait_for()
        self.page.keyboard.press("Escape")
        self.page.get_by_role("button", name="详情 Popover").click()
        self.page.get_by_text("不使用固定右侧栏。").wait_for()
        self.page.keyboard.press("Escape")
        self.page.get_by_role("button", name="显示骨架").click()
        self.page.get_by_label("正在加载看板").wait_for()
        after = self.page.evaluate("document.documentElement.scrollWidth")
        self.assertLessEqual(after, 390, f"Skeleton overflow: {after}")

if __name__ == "__main__":
    unittest.main(verbosity=2)
