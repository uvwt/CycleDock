"""Headless browser smoke tests against e2e/mock-server.mjs.

Run: MOCK_PORT=4190 node e2e/mock-server.mjs
     python e2e/smoke.py http://127.0.0.1:4190
This fixture is not a substitute for PostgreSQL integration testing.
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
        self.page = self.browser.new_page(viewport={"width": 1280, "height": 860})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))

    def tearDown(self):
        self.assertEqual(self.errors, [], "Browser JavaScript errors")
        self.page.close()

    def switch_project(self, name):
        self.page.get_by_role("button", name="选择项目").first.click()
        picker = self.page.get_by_role("dialog", name="选择项目")
        picker.wait_for()
        picker.get_by_role("button", name=name, exact=True).click()

    def open_card_menu(self):
        self.page.locator("main header").get_by_role("button", name="更多操作").click()
        menu = self.page.get_by_role("menu")
        menu.wait_for()
        return menu

    def open_task_details(self):
        menu = self.open_card_menu()
        menu.get_by_role("menuitem", name="任务详情").click()
        details = self.page.get_by_role("dialog", name="任务详情")
        details.wait_for()
        return details

    def test_discussion_workbench_composer_and_time_separator(self):
        self.page.goto(BASE + "/chat")
        self.page.get_by_role("heading", name="今天想推进什么？").wait_for()
        self.page.locator('aside[aria-label="讨论列表"]').wait_for()
        self.page.get_by_role(
            "button", name=re.compile("讨论服务器巡检计划")
        ).first.click()

        self.page.wait_for_url("**/chat/demo-project/discussion-1")
        self.page.get_by_text("我们需要每天自动检查服务器状态。").wait_for()
        self.page.get_by_text("建议监控 CPU、内存和容器健康").wait_for()

        self.assertEqual(self.page.locator("[data-time-separator]").count(), 1)
        self.assertEqual(
            self.page.locator('aside[aria-label="任务详情"]').count(), 0
        )
        menu = self.open_card_menu()
        menu.get_by_role("menuitem", name="编辑信息").click()
        self.page.get_by_role("dialog", name="编辑讨论信息").wait_for()
        self.page.keyboard.press("Escape")
        self.page.get_by_role("button", name="生成任务").wait_for()

        collapsed = self.page.locator(
            'input[aria-label="发送消息，继续推进任务…"]'
        )
        collapsed.wait_for()
        collapsed.focus()
        self.page.locator(
            'textarea[aria-label="发送消息，继续推进任务…"]'
        ).wait_for()

    def test_composers_preserve_newline_and_ime_confirmation(self):
        for path, placeholder in [
            ("/chat?project=demo-project", "发送消息，开始讨论…"),
            ("/chat/demo-project/discussion-1", "发送消息，继续推进任务…"),
        ]:
            self.page.goto(BASE + path)
            self.page.locator('input[aria-label="' + placeholder + '"]').focus()
            composer = self.page.locator('textarea[aria-label="' + placeholder + '"]')
            composer.fill("保留输入")
            composer.dispatch_event("keydown", {"key": "Enter", "isComposing": True, "bubbles": True})
            self.assertEqual(composer.input_value(), "保留输入")
            composer.press("End")
            composer.press("Shift+Enter")
            self.assertEqual(composer.input_value(), "保留输入\n")
            self.assertEqual(self.page.url, BASE + path)

    def test_new_discussion_button_only_opens_draft_until_first_send(self):
        create_requests = []
        self.page.on(
            "request",
            lambda request: create_requests.append(request.url)
            if request.method == "POST"
            and request.url.endswith("/api/v1/projects/demo-project/discussions")
            else None,
        )

        self.page.goto(BASE + "/chat/demo-project/discussion-1")
        self.page.get_by_role("button", name="新建讨论", exact=True).click()
        self.page.wait_for_url("**/chat?project=demo-project")
        collapsed = self.page.locator(
            'input[aria-label="发送消息，开始讨论…"]'
        )
        collapsed.wait_for()
        self.page.wait_for_timeout(100)
        self.assertEqual(create_requests, [])

        # 草稿首页的“新建讨论”同样只回到草稿入口，不得提前落库。
        self.page.get_by_role("button", name="新建讨论", exact=True).click()
        self.page.wait_for_timeout(100)
        self.assertEqual(create_requests, [])

        collapsed.focus()
        self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
        ).fill("只在发送第一句话时创建会话")
        self.page.get_by_role("button", name="发送", exact=True).click()
        self.page.get_by_text(
            "讨论已创建，但首条消息还未确认送达。"
        ).wait_for()
        self.assertEqual(len(create_requests), 1)

        self.page.get_by_role("button", name="重试发送").click()
        self.page.wait_for_url("**/chat/demo-project/discussion-*")
        self.assertEqual(len(create_requests), 1)

    def test_discussion_create_failure_keeps_draft_without_pending_card(self):
        self.page.goto(BASE + "/chat?project=demo-project")
        self.page.get_by_role(
            "button", name=re.compile("讨论服务器巡检计划")
        ).first.wait_for()
        rows = self.page.locator(
            'aside[aria-label="讨论列表"] button'
        )
        before = rows.count()
        collapsed = self.page.locator(
            'input[aria-label="发送消息，开始讨论…"]'
        )
        collapsed.focus()
        expanded = self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
        )
        expanded.fill("创建失败也不能丢草稿")
        fixture = self.page.request.post(
            BASE + "/api/v1/__fixture/fail-next-discussion"
        )
        self.assertEqual(fixture.status, 200)
        self.page.get_by_role("button", name="发送", exact=True).click()
        self.page.get_by_text("模拟 Discussion 创建失败", exact=True).wait_for()
        self.assertEqual(expanded.input_value(), "创建失败也不能丢草稿")
        self.assertEqual(rows.count(), before)
        self.assertEqual(
            self.page.get_by_role("button", name="重试发送").count(), 0
        )

    def test_discussion_draft_survives_project_switch(self):
        self.page.goto(BASE + "/chat?project=demo-project")
        collapsed = self.page.locator(
            'input[aria-label="发送消息，开始讨论…"]'
        )
        collapsed.focus()
        self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
        ).fill("切换项目后继续编辑这段草稿")

        self.switch_project("产品发布")
        self.page.wait_for_url("**/chat?project=demo-project-b")
        self.page.get_by_role(
            "heading", name="今天想推进什么？", exact=True
        ).wait_for()

        self.switch_project("NexusDock 运维")
        self.page.wait_for_url("**/chat?project=demo-project")
        restored = self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
        )
        restored.wait_for()
        self.assertEqual(
            restored.input_value(), "切换项目后继续编辑这段草稿"
        )

    def test_welcome_message_creates_discussion_and_sends_first_turn(self):
        self.page.goto(BASE + "/chat")
        collapsed = self.page.locator(
            'input[aria-label="发送消息，开始讨论…"]'
        )
        collapsed.wait_for()
        collapsed.focus()
        expanded = self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
        )
        expanded.fill("把服务器巡检交给 AI 负责")
        self.page.get_by_role("button", name="发送", exact=True).click()

        self.page.get_by_text(
            "讨论已创建，但首条消息还未确认送达。"
        ).wait_for()
        self.assertEqual(
            self.page.locator(
                'textarea[aria-label="发送消息，开始讨论…"]'
            ).input_value(),
            "把服务器巡检交给 AI 负责",
        )
        self.page.get_by_role("button", name="重试发送").click()
        self.page.wait_for_url("**/chat/demo-project/discussion-*")
        self.page.get_by_text("把服务器巡检交给 AI 负责").wait_for()

    def test_board_manual_todo_and_default_details(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_role("button", name="新建待办").click()
        dialog = self.page.get_by_role("dialog", name="新建待办")
        dialog.wait_for()
        dialog.get_by_role("textbox", name="待办标题").fill("手工填写待办")
        dialog.get_by_role("textbox", name="待办描述").fill(
            "这个待办不经过模型生成"
        )
        dialog.get_by_role("button", name="创建待办").click()

        self.page.wait_for_url("**/cards/demo-project/todo-*")
        self.page.get_by_role("heading", name="手工填写待办").first.wait_for()
        self.assertEqual(
            self.page.locator('aside[aria-label="任务详情"]').count(), 0
        )
        details = self.open_task_details()
        details.get_by_text("这个待办不经过模型生成").wait_for()

    def test_existing_todo_stream_and_desktop_details(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_role(
            "button", name=re.compile("检查 RN 服务器异常日志")
        ).first.click()

        self.page.wait_for_url("**/cards/demo-project/todo-1")
        status_switch = self.page.get_by_role(
            "button", name="切换任务状态"
        )
        status_switch.wait_for()
        status_switch.click()
        self.assertTrue(
            self.page.get_by_role(
                "menuitem", name="已完成", exact=True
            ).is_disabled()
        )
        self.page.keyboard.press("Escape")
        self.page.get_by_role("button", name="更多操作").wait_for()
        self.assertEqual(
            self.page.locator('aside[aria-label="任务详情"]').count(), 0
        )
        self.page.get_by_role("status", name="正在执行").first.wait_for()
        self.page.get_by_text("分析错误日志", exact=True).wait_for()
        self.page.get_by_role("button", name="取消排队").wait_for()
        self.page.get_by_role("button", name="重试", exact=True).wait_for()
        self.page.get_by_text("系统触发 / 未关联历史").wait_for()
        self.page.get_by_text("状态变化 · todo -> working").wait_for()
        self.page.get_by_text(
            "已定位 Docker 容器的错误记录。"
        ).wait_for(timeout=8000)

    def test_todo_results_preserve_event_order_without_duplicate_body(self):
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        self.page.get_by_role(
            "heading", name="检查 RN 服务器异常日志", exact=True
        ).wait_for()

        # 会话保留 Human A -> Human B -> Result A 的持久事件顺序，
        # 不将迟到结果移到较早输入之后；最终 Markdown 正文只能出现一次。
        turn_order = self.page.locator(
            "#event-event-9, #event-event-10, #event-event-3"
        ).evaluate_all("nodes => nodes.map(node => node.id)")
        self.assertEqual(
            turn_order,
            ["event-event-9", "event-event-3", "event-event-10"],
        )
        result = self.page.locator("#event-event-10")
        result.locator("strong").get_by_text(
            "上一轮日志检查完成", exact=True
        ).wait_for()
        self.assertEqual(
            self.page.get_by_text("上一轮日志检查完成", exact=True).count(), 1
        )

    def test_work_detail_sidebar_groups_and_info_alignment(self):
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        sidebar = self.page.locator('aside[aria-label="工作列表"]')
        sidebar.wait_for()

        working = sidebar.get_by_role("button", name=re.compile(r"^进行中"))
        todo = sidebar.get_by_role("button", name=re.compile(r"^待办"))
        review = sidebar.get_by_role("button", name=re.compile(r"^待审核"))
        done = sidebar.get_by_role("button", name=re.compile(r"^已完成"))
        ongoing = sidebar.get_by_role("button", name=re.compile(r"^持续事项"))

        self.assertEqual(working.get_attribute("aria-expanded"), "true")
        self.assertEqual(todo.get_attribute("aria-expanded"), "false")
        self.assertEqual(review.get_attribute("aria-expanded"), "false")
        self.assertEqual(done.get_attribute("aria-expanded"), "false")
        self.assertEqual(ongoing.get_attribute("aria-expanded"), "false")

        active_task = sidebar.get_by_role(
            "button", name=re.compile("检查 RN 服务器异常日志")
        )
        self.assertEqual(active_task.get_attribute("aria-current"), "page")

        actions = self.page.get_by_role("button", name="更多操作")
        header = actions.locator("xpath=ancestor::header[1]")
        actions_box = actions.bounding_box()
        header_box = header.bounding_box()
        self.assertIsNotNone(actions_box)
        self.assertIsNotNone(header_box)
        self.assertLess(
            (header_box["x"] + header_box["width"])
            - (actions_box["x"] + actions_box["width"]),
            32,
        )

        ongoing.click()
        self.assertEqual(working.get_attribute("aria-expanded"), "false")
        self.assertEqual(ongoing.get_attribute("aria-expanded"), "true")
        sidebar.get_by_role(
            "button", name=re.compile("每天检查服务器健康")
        ).click()
        self.page.wait_for_url("**/cards/demo-project/ongoing-1")
        next_sidebar = self.page.locator('aside[aria-label="工作列表"]')
        self.assertEqual(
            next_sidebar.get_by_role(
                "button", name=re.compile(r"^持续事项")
            ).get_attribute("aria-expanded"),
            "true",
        )

    def test_project_switch_keeps_work_and_chat_scopes_isolated(self):
        self.page.goto(BASE + "/board/demo-project")
        self.switch_project("产品发布")
        self.page.wait_for_url("**/board/demo-project-b")
        self.page.get_by_role(
            "button", name=re.compile("准备发布清单")
        ).first.wait_for()
        self.assertEqual(
            self.page.get_by_role(
                "button", name=re.compile("检查 RN 服务器异常日志")
            ).count(),
            0,
        )

        self.page.goto(BASE + "/chat?project=demo-project")
        self.switch_project("产品发布")
        self.page.wait_for_url("**/chat?project=demo-project-b")
        self.page.get_by_role(
            "button", name=re.compile("讨论版本发布")
        ).first.wait_for()
        self.assertEqual(
            self.page.get_by_role(
                "button", name=re.compile("讨论服务器巡检计划")
            ).count(),
            0,
        )

    def test_task_edit_failure_keeps_unsaved_input(self):
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        menu = self.open_card_menu()
        menu.get_by_role("menuitem", name="编辑信息").click()
        editor = self.page.get_by_role("dialog", name="编辑任务信息")
        editor.wait_for()
        title = editor.get_by_placeholder("任务标题")
        title.fill("保存失败仍保留")

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
        editor.get_by_role("button", name="保存", exact=True).click()
        self.page.get_by_text("模拟保存失败", exact=True).wait_for()
        self.assertEqual(title.input_value(), "保存失败仍保留")
        self.assertEqual(editor.get_by_placeholder("任务标题").count(), 1)
        self.page.unroute("**/api/v1/cards/todo-1", fail_card_patch)

    def test_ongoing_health_schedule_and_source_are_preserved(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_role(
            "button", name=re.compile("每天检查服务器健康")
        ).first.click()
        self.page.wait_for_url("**/cards/demo-project/ongoing-1")
        details = self.open_task_details()
        details.get_by_text("健康状态：healthy").wait_for()
        details.get_by_text("每天 09:00", exact=True).wait_for()
        details.get_by_text("讨论服务器巡检计划", exact=True).wait_for()
        details.get_by_role("button", name="新增日程").click()
        details.get_by_text("新增日程", exact=True).last.wait_for()
        details.get_by_role("button", name="取消").last.click()

    def test_schedule_validation_and_frequency_switch(self):
        self.page.goto(BASE + "/cards/demo-project/ongoing-1")
        details = self.open_task_details()
        details.get_by_role("button", name="新增日程").click()
        details.get_by_label("IANA 时区").fill("Not/AZone")
        details.get_by_role("button", name="保存", exact=True).click()
        details.get_by_role("alert").filter(has_text="IANA 时区").wait_for()
        details.get_by_label("IANA 时区").fill("Asia/Shanghai")
        details.get_by_label("频率", exact=True).select_option("monthly")
        details.get_by_label("日期（1–28）").fill("29")
        details.get_by_role("button", name="保存", exact=True).click()
        details.get_by_role("alert").filter(has_text="日期应为 1–28").wait_for()
        details.get_by_label("频率", exact=True).select_option("daily")
        with self.page.expect_request(lambda request: request.method == "POST" and request.url.endswith("/schedules")) as sent:
            details.get_by_role("button", name="保存", exact=True).click()
        self.assertNotIn("day_of_month", sent.value.post_data_json)
        self.assertNotIn("day_of_week", sent.value.post_data_json)

    def test_mcp_validation_token_retention_and_delete_cancel(self):
        self.page.goto(BASE + "/settings/mcp")
        self.page.get_by_role("button", name=re.compile("示例 MCP")).click()
        form = self.page.get_by_role("dialog", name="编辑 MCP 服务")
        form.get_by_label("请求超时（毫秒）").fill("999")
        form.get_by_role("button", name="测试", exact=True).click()
        form.get_by_role("alert").filter(has_text="1000").wait_for()
        form.get_by_label("请求超时（毫秒）").fill("30000")
        form.get_by_label("Bearer Token（可选）").fill("fixture-token")
        with self.page.expect_request(lambda request: request.method == "PUT" and "/mcp-connections/" in request.url):
            form.get_by_role("button", name="保存连接").click()
        form.wait_for(state="hidden")
        self.page.get_by_role("button", name=re.compile("示例 MCP")).click()
        self.assertEqual(form.get_by_label("Bearer Token（可选）").input_value(), "")
        form.get_by_label("清除已保存的 Token（改为匿名连接）").check()
        form.get_by_label("Bearer Token（可选）").fill("replacement")
        self.assertFalse(form.get_by_label("清除已保存的 Token（改为匿名连接）").is_checked())
        form.get_by_label("清除已保存的 Token（改为匿名连接）").check()
        self.assertEqual(form.get_by_label("Bearer Token（可选）").input_value(), "")
        with self.page.expect_request(lambda request: request.url.endswith("/agent/mcp-test")) as sent:
            form.get_by_role("button", name="测试", exact=True).click()
        self.assertTrue(sent.value.post_data_json["clear_bearer_token"])
        self.assertNotIn("enabled", sent.value.post_data_json)
        form.get_by_role("button", name="删除", exact=True).click()
        confirmation = self.page.get_by_role("dialog", name="删除 MCP 服务")
        confirmation.get_by_role("button", name="取消").click()
        form.wait_for()

    def test_shell_uses_logo_without_account_drawer(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_role("heading", name="工作", exact=True).wait_for()
        self.assertEqual(
            self.page.get_by_role("button", name="账户与工作空间").count(), 0
        )
        logo = self.page.get_by_role("button", name="CycleDock").locator("img")
        logo.wait_for()
        self.assertTrue(logo.get_attribute("src").endswith("cycledock-logo.png"))
        self.page.get_by_role("link", name="设置").click()
        self.page.get_by_role("heading", name="设置", exact=True).wait_for()
        self.page.get_by_role("link", name="账户", exact=True).wait_for()

    def test_mobile_discussion_drawer_and_actions_modal(self):
        self.page.set_viewport_size({"width": 375, "height": 812})
        self.page.goto(BASE + "/chat")
        self.page.get_by_role("heading", name="今天想推进什么？").wait_for()
        self.assertEqual(
            self.page.locator('nav[aria-label="主导航"]:visible').count(), 1
        )
        self.page.get_by_role("button", name="打开讨论列表").click()
        discussion_dialog = self.page.get_by_role("dialog", name="讨论列表")
        discussion_dialog.wait_for()
        discussion_dialog.get_by_role(
            "button", name=re.compile("讨论服务器巡检计划")
        ).click()

        self.page.wait_for_url("**/chat/demo-project/discussion-1")
        self.page.locator(
            'header nav[aria-label="主导航"]'
        ).wait_for(state="detached")
        self.assertEqual(
            self.page.locator('nav[aria-label="主导航"]:visible').count(), 0
        )
        self.page.get_by_role("button", name="打开讨论列表").wait_for()
        menu = self.open_card_menu()
        menu.get_by_role("menuitem", name="编辑信息").click()
        self.page.get_by_role("dialog", name="编辑讨论信息").wait_for()
        self.assertLessEqual(
            self.page.evaluate("document.documentElement.scrollWidth"), 390
        )

    def test_mobile_work_drawer_tracks_active_group(self):
        self.page.set_viewport_size({"width": 375, "height": 812})
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        self.assertEqual(
            self.page.locator('aside[aria-label="工作列表"]:visible').count(), 0
        )
        self.page.get_by_role("button", name="打开工作列表").click()
        drawer = self.page.get_by_role("dialog", name="工作列表")
        drawer.wait_for()
        work_sidebar = drawer.locator('aside[aria-label="工作列表"]')
        self.assertEqual(
            work_sidebar.get_by_role(
                "button", name=re.compile(r"^进行中")
            ).get_attribute("aria-expanded"),
            "true",
        )
        work_sidebar.get_by_role(
            "button", name=re.compile("检查 RN 服务器异常日志")
        ).wait_for()
        self.assertLessEqual(
            self.page.evaluate("document.documentElement.scrollWidth"), 390
        )

    def test_model_routes_can_add_reorder_and_save(self):
        self.page.goto(BASE + "/settings/models")
        self.page.get_by_role("heading", name="模型服务").wait_for()
        self.page.get_by_text("模型路由", exact=True).wait_for()
        self.page.get_by_text("优先级 1", exact=True).wait_for()

        self.page.get_by_role("button", name="添加 Route").click()
        self.page.get_by_text("优先级 2", exact=True).wait_for()
        self.page.get_by_role("button", name="下移模型 Route").first.click()

        with self.page.expect_request(
            lambda request: request.url.endswith("/api/v1/agent/settings")
            and request.method == "PUT"
        ) as request_info:
            self.page.get_by_role("button", name="保存模型路由").click()
        payload = request_info.value.post_data_json
        self.assertEqual(len(payload["routes"]), 2)
        self.assertEqual(payload["routes"][0]["model"], "")
        self.assertEqual(payload["routes"][1]["model"], "example-model")

    def test_settings_and_persistent_language_theme(self):
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
        surface = self.page.locator(".bg-canvas").first.evaluate(
            "(element) => getComputedStyle(element).backgroundColor"
        )
        self.assertNotEqual(surface, "rgb(248, 250, 252)")
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
            self.page.evaluate("document.documentElement.lang"), "en"
        )
        self.page.get_by_role("link", name="Chat", exact=True).click()
        self.page.get_by_role(
            "heading", name="What would you like to move forward today?"
        ).wait_for()
        self.page.locator(
            'button[aria-label="New discussion"]'
        ).wait_for()
        self.assertEqual(
            self.page.evaluate("document.documentElement.dataset.theme"),
            "dark",
        )

    def test_agent_settings_hide_runtime_controls(self):
        self.page.goto(BASE + "/settings/agent")
        self.page.get_by_role("heading", name="Agent 设置", exact=True).wait_for()
        self.page.get_by_text("内置 Agent", exact=True).wait_for()
        self.page.get_by_text("Workspace Agent Instructions", exact=True).wait_for()
        for removed in ("启用 Cycle Agent", "请求超时（毫秒）", "最大工具轮次"):
            self.assertEqual(
                self.page.get_by_text(removed, exact=True).count(),
                0,
                f"runtime-only setting is still visible: {removed}",
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
