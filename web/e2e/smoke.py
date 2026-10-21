"""Headless browser smoke tests for the project workbench.

Run after npm run build:
    set MOCK_PORT=4190 && node e2e/mock-server.mjs
    python e2e/smoke.py http://127.0.0.1:4190

The fixture validates browser behavior only. PostgreSQL/state-machine guarantees remain
covered by Go integration tests.
"""
import re
import sys
import unittest

from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:4188"
if len(sys.argv) > 1:
    sys.argv.pop(1)


class CycleDockSmoke(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.engine = sync_playwright().start()
        cls.browser = cls.engine.chromium.launch(headless=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.engine.stop()

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 1440, "height": 900})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))

    def tearDown(self):
        self.assertEqual(self.errors, [], "Browser JavaScript errors")
        self.page.close()

    def open_board(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_role(
            "heading", name="NexusDock 运维", exact=True
        ).wait_for()

    def open_discussion_home(self):
        self.page.goto(BASE + "/chat?project=demo-project")
        self.page.get_by_role(
            "heading", name="今天想推进什么？", exact=True
        ).wait_for()
        self.page.locator('aside[aria-label="讨论列表"]:visible').wait_for()

    def test_board_is_primary_workbench_without_legacy_nav(self):
        self.open_board()

        self.assertEqual(
            self.page.locator('nav[aria-label="主导航"]:visible').count(), 1
        )
        self.page.get_by_role("button", name="讨论", exact=True).wait_for()
        self.page.get_by_role("button", name="工作", exact=True).wait_for()
        self.page.locator('nav[aria-label="工作视图"]:visible').wait_for()
        self.page.get_by_role("button", name=re.compile("全部工作")).wait_for()
        self.page.get_by_role("button", name="新建待办").wait_for()

        self.page.locator('summary[aria-label="账户菜单"]').click()
        self.page.get_by_role("button", name="演示空间", exact=True).wait_for()
        self.page.get_by_role("button", name="第二空间", exact=True).wait_for()
        self.page.get_by_role("button", name="设置", exact=True).wait_for()
        self.page.get_by_role("button", name="退出登录", exact=True).wait_for()

    def test_project_switch_changes_board_and_discussion_scope(self):
        self.open_board()
        project_menu = self.page.locator('summary[aria-label="项目菜单"]')
        project_menu.click()
        self.page.get_by_role("button", name="产品发布", exact=True).click()
        self.page.wait_for_url("**/board/demo-project-b")
        self.page.get_by_role("heading", name="产品发布", exact=True).wait_for()
        self.page.get_by_role(
            "button", name=re.compile("准备发布清单")
        ).first.wait_for()
        self.assertEqual(
            self.page.get_by_role(
                "button", name=re.compile("检查 RN 服务器异常日志")
            ).count(),
            0,
        )

        project_menu = self.page.locator('summary[aria-label="项目菜单"]')
        project_menu.click()
        self.page.get_by_role("button", name="NexusDock 运维", exact=True).click()
        self.page.wait_for_url("**/board/demo-project")
        self.page.get_by_role(
            "button", name=re.compile("检查 RN 服务器异常日志")
        ).first.wait_for()
        self.assertEqual(
            self.page.get_by_role("button", name=re.compile("准备发布清单")).count(),
            0,
        )

    def test_todo_execution_is_attached_to_source_messages_and_details_are_read_only(self):
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        self.page.get_by_role(
            "heading", name="检查 RN 服务器异常日志", exact=True
        ).wait_for()

        failed_message = self.page.locator('[data-trigger-id="trigger-failed"]')
        failed_message.get_by_text("执行失败", exact=True).wait_for()
        failed_message.get_by_role("button", name="重试", exact=True).wait_for()

        queued_message = self.page.locator('[data-trigger-id="trigger-queued"]')
        queued_message.get_by_text("等待执行", exact=True).wait_for()
        queued_message.get_by_role(
            "button", name="取消排队", exact=True
        ).wait_for()

        self.page.get_by_text("系统触发 / 未关联历史", exact=True).wait_for()
        self.page.get_by_text("执行完成", exact=True).wait_for()
        self.assertEqual(
            self.page.get_by_role("dialog", name="任务信息").count(), 0
        )

        self.page.get_by_role("button", name="任务信息", exact=True).click()
        dialog = self.page.get_by_role("dialog", name="任务信息")
        dialog.wait_for()
        dialog.get_by_text("讨论服务器巡检计划", exact=True).wait_for()
        self.assertEqual(dialog.get_by_placeholder("任务标题").count(), 0)

        dialog.get_by_role("button", name="编辑任务信息", exact=True).click()
        title = dialog.get_by_placeholder("任务标题")
        title.wait_for()
        title.fill("临时标题")
        dialog.get_by_role("button", name="取消", exact=True).click()
        self.assertEqual(dialog.get_by_placeholder("任务标题").count(), 0)

        def fail_card_patch(route):
            if route.request.method == "PATCH":
                route.fulfill(
                    status=500,
                    content_type="application/json",
                    body='{"error":{"code":"fixture_save_failure","message":"模拟保存失败"}}',
                )
            else:
                route.continue_()

        self.page.route("**/api/v1/cards/todo-1", fail_card_patch)
        dialog.get_by_role("button", name="编辑任务信息", exact=True).click()
        failed_title = dialog.get_by_placeholder("任务标题")
        failed_title.fill("保存失败仍保留")
        dialog.get_by_role("button", name="保存", exact=True).click()
        self.page.get_by_text("模拟保存失败", exact=True).wait_for()
        self.assertEqual(failed_title.input_value(), "保存失败仍保留")
        self.assertEqual(dialog.get_by_placeholder("任务标题").count(), 1)
        self.page.unroute("**/api/v1/cards/todo-1", fail_card_patch)

    def test_new_discussion_create_failure_keeps_draft_without_card(self):
        self.open_discussion_home()
        sidebar_rows = self.page.locator(
            'aside[aria-label="讨论列表"] > div:nth-of-type(2) > div > button'
        )
        before = sidebar_rows.count()
        self.page.get_by_role("button", name="新建讨论", exact=True).first.click()
        composer = self.page.get_by_placeholder("从一句话开始新的讨论…")
        composer.fill("创建失败也不能丢草稿")
        fixture = self.page.request.post(
            BASE + "/api/v1/__fixture/fail-next-discussion"
        )
        self.assertEqual(fixture.status, 200)
        self.page.get_by_role("button", name="发送", exact=True).click()
        self.page.get_by_text("模拟 Discussion 创建失败", exact=True).wait_for()
        self.assertEqual(composer.input_value(), "创建失败也不能丢草稿")
        self.assertEqual(sidebar_rows.count(), before)
        self.assertEqual(
            self.page.get_by_role("button", name="重试发送", exact=True).count(),
            0,
        )

    def test_new_discussion_first_message_failure_retries_same_card(self):
        self.open_discussion_home()
        sidebar_rows = self.page.locator(
            'aside[aria-label="讨论列表"] > div:nth-of-type(2) > div > button'
        )
        before = sidebar_rows.count()

        self.page.get_by_role("button", name="新建讨论", exact=True).first.click()
        composer = self.page.get_by_placeholder("从一句话开始新的讨论…")
        composer.fill("验证首条消息失败后安全重试")
        self.page.get_by_role("button", name="发送", exact=True).click()

        self.page.get_by_text(
            "讨论已创建，但首条消息还未确认送达。重试会复用同一 Card 和同一幂等键，不会重复创建讨论。",
            exact=True,
        ).wait_for()
        self.assertEqual(sidebar_rows.count(), before + 1)
        self.assertEqual(composer.input_value(), "验证首条消息失败后安全重试")

        self.page.get_by_role("button", name="重试发送", exact=True).click()
        self.page.wait_for_url("**/chat/demo-project/discussion-*")
        self.page.get_by_text(
            "验证首条消息失败后安全重试", exact=True
        ).wait_for()

    def test_new_discussion_draft_survives_project_switch(self):
        self.open_discussion_home()
        composer = self.page.get_by_placeholder("从一句话开始新的讨论…")
        composer.fill("切换项目后继续编辑这段草稿")

        self.page.locator('summary[aria-label="项目菜单"]').click()
        self.page.get_by_role("button", name="产品发布", exact=True).click()
        self.page.wait_for_url("**/chat?project=demo-project-b")
        self.page.get_by_role(
            "heading", name="今天想推进什么？", exact=True
        ).wait_for()

        self.page.locator('summary[aria-label="项目菜单"]').click()
        self.page.get_by_role("button", name="NexusDock 运维", exact=True).click()
        self.page.wait_for_url("**/chat?project=demo-project")
        restored = self.page.get_by_placeholder("从一句话开始新的讨论…")
        restored.wait_for()
        self.assertEqual(restored.input_value(), "切换项目后继续编辑这段草稿")

    def test_direct_url_refresh_back_and_forward_preserve_project_context(self):
        self.open_board()
        self.page.get_by_role(
            "button", name=re.compile("检查 RN 服务器异常日志")
        ).first.click()
        self.page.wait_for_url("**/cards/demo-project/todo-1")
        self.page.get_by_role(
            "heading", name="检查 RN 服务器异常日志", exact=True
        ).wait_for()

        self.page.reload()
        self.page.get_by_role(
            "heading", name="检查 RN 服务器异常日志", exact=True
        ).wait_for()
        self.page.go_back()
        self.page.wait_for_url("**/board/demo-project")
        self.page.get_by_role(
            "heading", name="NexusDock 运维", exact=True
        ).wait_for()
        self.page.go_forward()
        self.page.wait_for_url("**/cards/demo-project/todo-1")
        self.page.get_by_role(
            "heading", name="检查 RN 服务器异常日志", exact=True
        ).wait_for()

    def test_manual_todo_creation_remains_available(self):
        self.open_board()
        self.page.get_by_role("button", name="新建待办", exact=True).click()
        dialog = self.page.get_by_role("dialog", name="新建待办")
        dialog.get_by_role("textbox", name="待办标题").fill("手工填写待办")
        dialog.get_by_role("textbox", name="待办描述").fill(
            "这个待办不经过模型生成"
        )
        dialog.get_by_role("button", name="创建待办", exact=True).click()
        self.page.wait_for_url("**/cards/demo-project/todo-*")
        self.page.get_by_role(
            "heading", name="手工填写待办", exact=True
        ).wait_for()
        self.assertEqual(
            self.page.get_by_role("dialog", name="任务信息").count(), 0
        )
        self.page.get_by_role("button", name="任务信息", exact=True).click()
        self.page.get_by_role("dialog", name="任务信息").get_by_text(
            "这个待办不经过模型生成", exact=True
        ).wait_for()

    def test_mobile_uses_shared_navigation_and_task_info_popover(self):
        self.page.set_viewport_size({"width": 375, "height": 812})
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        self.page.get_by_role(
            "heading", name="检查 RN 服务器异常日志", exact=True
        ).wait_for()

        self.assertEqual(
            self.page.locator('aside[aria-label="主导航"]:visible').count(), 0
        )
        self.page.get_by_role("button", name="打开导航", exact=True).click()
        navigation_dialog = self.page.get_by_role("dialog", name="CycleDock")
        navigation_dialog.wait_for()
        navigation_dialog.get_by_role("button", name="工作", exact=True).wait_for()
        navigation_dialog.get_by_role("button", name="关闭").click()

        self.page.get_by_role("button", name="任务信息", exact=True).click()
        detail_dialog = self.page.get_by_role("dialog", name="任务信息")
        detail_dialog.wait_for()
        detail_dialog.get_by_role(
            "button", name="编辑任务信息", exact=True
        ).wait_for()
        self.assertLessEqual(
            self.page.evaluate("document.documentElement.scrollWidth"), 390
        )

    def test_ongoing_schedule_is_readable_before_editing(self):
        self.page.goto(BASE + "/cards/demo-project/ongoing-1")
        self.page.get_by_role(
            "heading", name="每天检查服务器健康", exact=True
        ).wait_for()
        self.page.get_by_role("button", name="任务信息", exact=True).click()
        dialog = self.page.get_by_role("dialog", name="任务信息")
        dialog.get_by_text("每天 09:00", exact=True).wait_for()
        self.assertEqual(dialog.get_by_role("combobox", name="频率").count(), 0)
        dialog.get_by_role("button", name="编辑", exact=True).last.click()
        dialog.get_by_role("combobox", name="频率").wait_for()

    def test_settings_keep_language_and_theme_with_shared_workbench(self):
        self.page.goto(BASE + "/settings/general")
        self.page.get_by_role("combobox", name="界面语言").select_option("en")
        self.page.get_by_role("heading", name="Settings", exact=True).wait_for()
        self.page.get_by_role("combobox", name="Appearance").select_option(
            "dark"
        )
        self.assertEqual(
            self.page.evaluate("document.documentElement.dataset.theme"),
            "dark",
        )
        self.page.reload()
        self.page.get_by_role("heading", name="Settings", exact=True).wait_for()
        self.assertEqual(
            self.page.get_by_role(
                "combobox", name="Appearance"
            ).input_value(),
            "dark",
        )
        self.assertEqual(
            self.page.get_by_role("combobox", name="Language").input_value(),
            "en",
        )
        self.assertEqual(
            self.page.locator('nav[aria-label="Main navigation"]:visible').count(),
            1,
        )

    def test_workspace_switch_is_available_from_account_menu(self):
        self.open_board()
        self.page.locator('summary[aria-label="账户菜单"]').click()
        self.page.get_by_role("button", name="第二空间", exact=True).click()
        self.page.get_by_text("已切换工作空间", exact=True).wait_for()
        self.page.wait_for_url("**/board**")
        self.page.locator('summary[aria-label="账户菜单"]').click()
        self.page.get_by_role("button", name="第二空间", exact=True).wait_for()


if __name__ == "__main__":
    unittest.main(verbosity=2)
