"""Headless browser smoke tests against e2e/mock-server.mjs.

Run: MOCK_PORT=4190 node e2e/mock-server.mjs
     python e2e/smoke.py http://127.0.0.1:4190
This fixture is not a substitute for PostgreSQL integration testing.
"""
import copy
import re
import sys
import unittest

from playwright.sync_api import expect, sync_playwright

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
        picker = self.page.get_by_role("menu")
        picker.wait_for()
        picker.get_by_role("menuitem", name=name, exact=True).click()

    def card_action_button(self):
        # 侧边栏每一项也有“更多操作”；只操作当前 Card 顶部的操作按钮。
        return self.page.locator("main section > div.absolute").get_by_role(
            "button", name="更多操作"
        )

    def open_card_menu(self):
        self.card_action_button().click()
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
        # 旧「生成任务」按钮已从会话工作台移除；编辑完应回到可输入状态。
        self.page.get_by_role("dialog", name="编辑讨论信息").wait_for(state="hidden")

        collapsed = self.page.locator(
            'textarea[aria-label="发送消息，继续推进任务…"]'
        )
        collapsed.wait_for()
        collapsed.focus()
        self.page.locator(
            'textarea[aria-label="发送消息，继续推进任务…"]'
        ).wait_for()

    def test_confirmation_uses_safe_focus_and_keeps_failed_delete_open(self):
        self.page.goto(BASE + "/chat/demo-project/discussion-1")
        self.open_card_menu().get_by_role("menuitem", name="删除讨论").click()
        confirmation = self.page.get_by_role("alertdialog", name="删除这个讨论？")
        cancel = confirmation.get_by_role("button", name="取消")
        cancel.wait_for()
        self.page.wait_for_function("document.activeElement?.textContent === '取消'")
        self.page.keyboard.press("Tab")
        self.assertTrue(confirmation.get_by_role("button", name="删除讨论", exact=True).evaluate("node => node === document.activeElement"))
        self.page.keyboard.press("Tab")
        self.assertTrue(cancel.evaluate("node => node === document.activeElement"))
        self.page.mouse.click(5, 5)
        self.assertTrue(confirmation.is_visible())

        pending_delete = []
        def hold_delete(route):
            if route.request.method == "DELETE":
                pending_delete.append(route)
            else:
                route.continue_()

        self.page.route("**/api/v1/cards/discussion-1", hold_delete)
        confirmation.get_by_role("button", name="删除讨论", exact=True).click()
        self.page.wait_for_function("document.querySelector('[role=alertdialog]')?.getAttribute('aria-busy') === 'true'")
        self.page.keyboard.press("Escape")
        self.assertTrue(confirmation.is_visible())
        self.assertFalse(cancel.is_enabled())
        pending_delete[0].fulfill(status=500, content_type="application/json", body='{"error":{"code":"fixture_failure","message":"模拟删除失败"}}')
        self.page.get_by_text("模拟删除失败", exact=True).wait_for()
        self.assertTrue(confirmation.is_visible())
        self.assertTrue(cancel.is_enabled())
        cancel.click()
        confirmation.wait_for(state="hidden")

    def test_edit_dialog_scrolls_on_small_screen_and_closes_with_escape(self):
        self.page.set_viewport_size({"width": 375, "height": 520})
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        self.open_card_menu().get_by_role("menuitem", name="编辑信息").click()
        editor = self.page.get_by_role("dialog", name="编辑任务信息")
        title = editor.get_by_placeholder("任务标题")
        title.wait_for()
        self.page.wait_for_function("document.activeElement?.getAttribute('placeholder') === '任务标题'")
        self.assertGreaterEqual(editor.get_by_placeholder("目标、范围与必要背景").evaluate("node => node.getBoundingClientRect().height"), 100)
        editor.get_by_role("button", name="保存", exact=True).scroll_into_view_if_needed()
        self.assertLessEqual(self.page.evaluate("document.documentElement.scrollWidth"), 375)
        self.page.keyboard.press("Escape")
        editor.wait_for(state="hidden")
        self.card_action_button().focus()
        self.page.keyboard.press("Enter")
        self.page.get_by_role("menu").wait_for()

    def test_composers_preserve_newline_and_ime_confirmation(self):
        for path, placeholder in [
            ("/chat?project=demo-project", "发送消息，开始讨论…"),
            ("/chat/demo-project/discussion-1", "发送消息，继续推进任务…"),
        ]:
            self.page.goto(BASE + path)
            self.page.locator('textarea[aria-label="' + placeholder + '"]').focus()
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
            'textarea[aria-label="发送消息，开始讨论…"]'
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
            'textarea[aria-label="发送消息，开始讨论…"]'
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
            'textarea[aria-label="发送消息，开始讨论…"]'
        )
        collapsed.focus()
        self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
        ).fill("切换项目后继续编辑这段草稿")
        # React useEffect 异步持久化草稿；在切项目之前等待真实保存完成，
        # 而不是依赖宿主机器恰好在点击之间有足够的空闲时间。
        self.page.wait_for_function(
            "() => JSON.parse(localStorage.getItem('cycledock.new-discussion.drafts.v2') || '{}')['demo-project']?.content === '切换项目后继续编辑这段草稿'"
        )

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
        # URL 先于 ProjectContext 组件渲染更新，必须等待项目完成切换。
        expect(restored).to_have_value("切换项目后继续编辑这段草稿", timeout=10000)

    def test_welcome_message_creates_discussion_and_sends_first_turn(self):
        self.page.goto(BASE + "/chat")
        collapsed = self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
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
        self.page.get_by_text("手工填写待办", exact=True).first.wait_for()
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
        self.card_action_button().wait_for()
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

    def test_composer_uses_one_stop_or_send_button(self):
        original = self.page.request.get(BASE + "/api/v1/cards/todo-1").json()
        state = {"stopped": False}
        comments = []
        def detail(route):
            payload = copy.deepcopy(original)
            if state["stopped"]:
                payload["current_attempt"] = None
            route.fulfill(json=payload)
        def stop(route):
            state["stopped"] = True
            route.fulfill(status=204)
        self.page.route("**/api/v1/cards/todo-1", detail)
        self.page.route("**/attempts/attempt-running/cancel", stop)
        self.page.on("request", lambda request: comments.append(request) if request.method == "POST" and request.url.endswith("/comments") else None)
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        composer = self.page.locator("main form")
        composer.get_by_role("button", name="中止", exact=True).wait_for()
        self.assertEqual(composer.get_by_role("button").count(), 1)
        text = composer.get_by_role("textbox")
        text.fill("后续要求")
        self.assertEqual(composer.get_by_role("button", name="中止", exact=True).count(), 0)
        self.assertTrue(composer.get_by_role("button", name="发送", exact=True).is_enabled())
        text.fill("")
        with self.page.expect_request(lambda request: request.url.endswith("/attempts/attempt-running/cancel")):
            composer.get_by_role("button", name="中止", exact=True).click()
        composer.get_by_role("button", name="发送", exact=True).wait_for()
        self.assertEqual(composer.get_by_role("button").count(), 1)
        self.assertEqual(comments, [], "stop must not submit a message")
        self.assertTrue(composer.get_by_role("button", name="发送", exact=True).is_disabled())

    def test_running_bubble_follows_sent_assistant_messages(self):
        self.page.route("**/api/v1/cards/todo-1/stream", lambda route: route.fulfill(
            status=200, content_type="text/event-stream", body=": waiting\n\n"
        ))
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        running = self.page.locator('[data-execution-id="attempt-running"]')
        running.wait_for()
        self.page.locator('[data-queued-turn="turn-queued"]').wait_for()
        reply = self.page.locator("#event-event-10")
        queue = self.page.get_by_role("region", name="待执行队列")
        reply_box = reply.bounding_box()
        running_box = running.bounding_box()
        queue_box = queue.bounding_box()
        self.assertLessEqual(reply_box["y"] + reply_box["height"], running_box["y"])
        self.assertLessEqual(running_box["y"] + running_box["height"], queue_box["y"])
        self.assertEqual(self.page.get_by_role("status", name="正在执行").count(), 1)

    def mock_queue_transition(self):
        state = {"mode": "pending"}
        original = self.page.request.get(BASE + "/api/v1/cards/todo-1").json()

        def card_detail(route):
            detail = copy.deepcopy(original)
            if state["mode"] != "pending":
                detail["pending_turns"] = []
            if state["mode"] == "cancelled":
                detail["timeline"] = [event for event in detail["timeline"] if event["id"] != "event-7"]
            if state["mode"] == "claimed":
                detail["current_attempt"].update({
                    "id": "attempt-steered", "turn_id": "turn-queued",
                    "source_event_id": "event-7", "status": "running",
                })
            route.fulfill(json=detail)

        self.page.route("**/api/v1/cards/todo-1", card_detail)
        return state

    def test_queued_input_uses_compact_rows_and_steers_original_turn(self):
        state = self.mock_queue_transition()
        def steer(route):
            state["mode"] = "claimed"
            route.fulfill(status=204)
        self.page.route("**/turns/turn-queued/steer", steer)
        comments = []
        self.page.on("request", lambda request: comments.append(request) if request.method == "POST" and request.url.endswith("/comments") else None)
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        row = self.page.locator('[data-queued-turn="turn-queued"]')
        row.wait_for()
        timeline = self.page.get_by_role("log", name="统一消息时间线")
        self.assertEqual(timeline.get_by_text("顺便检查磁盘空间。", exact=True).count(), 0)
        self.assertEqual(self.page.locator("#event-event-7").count(), 0)
        self.assertEqual(row.locator("article").count(), 0)
        row_box = row.bounding_box()
        composer_box = self.page.locator("main form").bounding_box()
        self.assertLessEqual(row_box["y"] + row_box["height"], composer_box["y"] + 2)
        row.get_by_role("button", name="编辑内容").click()
        editor = self.page.get_by_role("dialog", name="编辑内容")
        self.assertEqual(editor.get_by_role("textbox", name="排队输入").input_value(), "顺便检查磁盘空间。")
        self.page.keyboard.press("Escape")
        with self.page.expect_request(lambda request: request.url.endswith("/turns/turn-queued/steer")):
            row.get_by_role("button", name="引导", exact=True).click()
        row.wait_for(state="detached")
        timeline.get_by_text("顺便检查磁盘空间。", exact=True).wait_for()
        self.assertEqual(self.page.locator("#event-event-7").count(), 1)
        self.assertEqual(comments, [], "steer must reuse the existing Turn")

    def test_queued_input_edit_saves_original_item_and_keeps_failed_draft(self):
        original = self.page.request.get(BASE + "/api/v1/cards/todo-1").json()
        state = {"content": "顺便检查磁盘空间。", "attempts": 0}
        def detail(route):
            payload = copy.deepcopy(original)
            payload["pending_turns"][0]["content"] = state["content"]
            for event in payload["timeline"]:
                if event["id"] == "event-7":
                    event["content"] = state["content"]
            route.fulfill(json=payload)
        def edit(route):
            self.assertEqual(route.request.method, "PATCH")
            state["attempts"] += 1
            if state["attempts"] == 1:
                route.fulfill(status=409, json={"error": {"message": "模拟保存失败"}})
            else:
                state["content"] = route.request.post_data_json["content"]
                route.fulfill(status=204)
        self.page.route("**/api/v1/cards/todo-1", detail)
        self.page.route("**/api/v1/cards/todo-1/turns/turn-queued", edit)
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        row = self.page.locator('[data-queued-turn="turn-queued"]')
        row.wait_for()
        row.get_by_role("button", name="编辑内容").click()
        editor = self.page.get_by_role("dialog", name="编辑内容")
        content = editor.get_by_role("textbox", name="排队输入")
        content.fill("   ")
        self.assertTrue(editor.get_by_role("button", name="保存", exact=True).is_disabled())
        content.fill("更新后的排队要求")
        editor.get_by_role("button", name="保存", exact=True).click()
        editor.get_by_role("alert").get_by_text("模拟保存失败", exact=True).wait_for()
        self.assertEqual(content.input_value(), "更新后的排队要求")
        editor.get_by_role("button", name="保存", exact=True).click()
        editor.wait_for(state="hidden")
        row.get_by_role("button", name="更新后的排队要求", exact=True).wait_for()
        self.assertEqual(self.page.locator("#event-event-7").count(), 0)
        self.page.reload()
        row.get_by_role("button", name="更新后的排队要求", exact=True).wait_for()
        row.get_by_role("button", name="编辑内容").click()
        editor.get_by_role("textbox", name="排队输入").fill("不保存的草稿")
        editor.get_by_role("button", name="取消", exact=True).click()
        row.get_by_role("button", name="更新后的排队要求", exact=True).wait_for()
        self.assertEqual(state["attempts"], 2)

    def test_mobile_queue_delete_preserves_failed_row_and_removes_cancelled_bubble(self):
        self.page.set_viewport_size({"width": 375, "height": 812})
        state = self.mock_queue_transition()
        def cancel(route):
            if state.get("failed"):
                state["mode"] = "cancelled"
                route.fulfill(status=204)
            else:
                state["failed"] = True
                route.fulfill(status=409, json={"error": {"message": "模拟取消失败"}})
        self.page.route("**/turns/turn-queued/cancel", cancel)
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        row = self.page.locator('[data-queued-turn="turn-queued"]')
        row.wait_for()
        self.assertLessEqual(self.page.evaluate("document.documentElement.scrollWidth"), 375)
        row.get_by_role("button", name="取消排队").click()
        self.page.get_by_text("模拟取消失败", exact=True).wait_for()
        row.get_by_role("button", name="取消排队").click()
        row.wait_for(state="detached")
        self.assertEqual(self.page.locator("#event-event-7").count(), 0)
        self.page.reload()
        self.card_action_button().wait_for()
        self.assertEqual(self.page.locator('[data-queued-turn="turn-queued"]').count(), 0)
        self.assertEqual(self.page.locator("#event-event-7").count(), 0)

    def test_todo_results_preserve_event_order_without_duplicate_body(self):
        self.page.goto(BASE + "/cards/demo-project/todo-1")
        self.page.get_by_role("log", name="统一消息时间线").wait_for()

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
        self.assertEqual(sidebar.get_by_role("button", name=re.compile(r"^持续事项")).count(), 0)

        self.assertEqual(working.get_attribute("aria-expanded"), "true")
        self.assertEqual(todo.get_attribute("aria-expanded"), "false")
        self.assertEqual(review.get_attribute("aria-expanded"), "false")
        self.assertEqual(done.get_attribute("aria-expanded"), "false")

        active_task = sidebar.get_by_role(
            "button", name=re.compile("检查 RN 服务器异常日志")
        )
        self.assertEqual(active_task.get_attribute("aria-current"), "page")

        actions = self.page.get_by_role("button", name="更多操作")
        actions_box = actions.bounding_box()
        self.assertIsNotNone(actions_box)
        self.assertLess(1280 - (actions_box["x"] + actions_box["width"]), 32)
        todo.click()
        self.assertEqual(working.get_attribute("aria-expanded"), "false")
        self.assertEqual(todo.get_attribute("aria-expanded"), "true")

    def test_plans_own_ongoing_work_and_detail_navigation(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.locator('a[href="/plans/demo-project"]').first.wait_for()
        self.assertEqual(self.page.get_by_role("button", name=re.compile("每天检查服务器健康")).count(), 0)
        self.page.get_by_role("link", name="计划", exact=True).first.click()
        self.page.wait_for_url("**/plans/demo-project")
        self.page.get_by_role("button", name=re.compile("每天检查服务器健康")).click()
        self.page.wait_for_url("**/plans/demo-project/ongoing-1")
        sidebar = self.page.locator('aside[aria-label="计划列表"]')
        sidebar.wait_for()
        self.assertEqual(sidebar.get_by_role("button", name=re.compile("每天检查服务器健康")).get_attribute("aria-current"), "page")
        self.assertEqual(self.page.locator('aside[aria-label="工作列表"]').count(), 0)
        self.page.get_by_role("link", name="计划", exact=True).first.click()
        self.assertFalse(sidebar.is_visible())
        self.page.get_by_role("link", name="计划", exact=True).first.click()
        sidebar.wait_for()
        self.switch_project("产品发布")
        self.page.wait_for_url("**/plans/demo-project-b")
        self.page.get_by_text("这个项目还没有计划", exact=True).wait_for()

    def test_plans_include_unscheduled_work_and_filter_by_lifecycle(self):
        self.page.route("**/api/v1/projects/demo-project/schedules", lambda route: route.fulfill(json=[]))
        self.page.goto(BASE + "/plans/demo-project")
        self.page.get_by_text("未设置自动执行时间", exact=True).wait_for()
        self.page.get_by_role("button", name="已暂停", exact=True).click()
        self.page.get_by_text("这个筛选下没有计划", exact=True).wait_for()
        self.page.get_by_role("button", name="运行中", exact=True).click()
        self.page.get_by_role("button", name=re.compile("每天检查服务器健康")).wait_for()
        self.page.goto(BASE + "/cards/demo-project/ongoing-1")
        self.page.wait_for_url("**/plans/demo-project/ongoing-1")

    def test_activity_agents_filter_server_results_and_preserve_status(self):
        self.page.goto(BASE + "/activity/demo-project")
        agents = self.page.get_by_role("complementary", name="Agent 列表")
        agents.get_by_role("button", name="Cycle", exact=True).wait_for()
        agents.get_by_role("button", name="Claude Code", exact=True).wait_for()
        activity = self.page.get_by_role("region", name="活动列表")
        activity.get_by_text("Cycle 正在检查日志", exact=True).wait_for()
        with self.page.expect_request(lambda request:
            "/activity?" in request.url and "source_id=22222222" in request.url
        ):
            agents.get_by_role("button", name="Codex", exact=True).click()
        activity.get_by_text("Codex 已完成巡检", exact=True).wait_for()
        self.assertEqual(activity.get_by_text("Cycle 正在检查日志", exact=True).count(), 0)
        self.page.get_by_role("button", name="已完成", exact=True).click()
        self.assertIn("source_id=22222222", self.page.url)
        self.page.reload()
        activity.get_by_text("Codex 已完成巡检", exact=True).wait_for()
        self.assertEqual(agents.get_by_role("button", name="Codex", exact=True).get_attribute("aria-pressed"), "true")
        agents.get_by_role("button", name="Claude Code", exact=True).click()
        self.assertIn("view=completed", self.page.url)
        self.page.get_by_text("这个筛选下没有活动", exact=True).wait_for()
        agents.get_by_role("button", name="全部来源", exact=True).click()
        activity.get_by_text("Codex 已完成巡检", exact=True).wait_for()
        self.assertNotIn("source_id=", self.page.url)
        self.switch_project("产品发布")
        self.page.wait_for_url("**/activity/demo-project-b")
        self.page.get_by_text("这个项目还没有活动", exact=True).wait_for()

    def test_mobile_activity_agent_filter_and_plan_navigation(self):
        self.page.set_viewport_size({"width": 375, "height": 812})
        self.page.goto(BASE + "/activity/demo-project")
        agents = self.page.get_by_role("complementary", name="Agent 列表")
        agents.get_by_role("button", name="Codex", exact=True).click()
        activity = self.page.get_by_role("region", name="活动列表")
        activity.get_by_text("Codex 已完成巡检", exact=True).wait_for()
        self.assertLessEqual(self.page.evaluate("document.documentElement.scrollWidth"), 375)
        activity.get_by_role("button", name=re.compile("每天检查服务器健康")).click()
        self.page.wait_for_url("**/plans/demo-project/ongoing-1")
        self.page.get_by_role("button", name="打开计划列表").click()
        self.page.get_by_role("dialog", name="计划列表").wait_for()

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
        self.page.goto(BASE + "/plans/demo-project")
        self.page.get_by_role(
            "button", name=re.compile("每天检查服务器健康")
        ).first.click()
        self.page.wait_for_url("**/plans/demo-project/ongoing-1")
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
        confirmation = self.page.get_by_role("alertdialog", name="删除 MCP 服务")
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
        # 测试环境的 mock-server 同进程持久化 settings，重复执行也必须从固定基线开始。
        current = self.page.request.get(BASE + "/api/v1/agent/settings").json()
        current["routes"] = [{
            "id": "00000000-0000-4000-8000-000000000101",
            "enabled": True,
            "provider_type": "openai_compatible",
            "credential_id": "",
            "provider_base_url": "https://api.example.com/v1",
            "model": "example-model",
            "api_key_env": "CYCLEDOCK_MODEL_API_KEY",
        }]
        reset = self.page.request.put(BASE + "/api/v1/agent/settings", data=current)
        self.assertTrue(reset.ok, "reset fixture model routes")
        self.page.goto(BASE + "/settings/models")
        self.page.get_by_role("heading", name="模型服务").wait_for()
        self.page.get_by_text("模型路由", exact=True).wait_for()
        handles = self.page.get_by_role("button", name="拖拽调整优先级")
        self.assertEqual(handles.count(), 1)

        # 目前通过现有服务商添加 Route，通过 dnd-kit 键盘传感器调整优先级。
        self.page.get_by_role(
            "button", name="为此服务商添加一条模型 Route"
        ).click()
        self.assertEqual(handles.count(), 2)
        handles.nth(1).focus()
        self.page.keyboard.press("Space")
        self.page.wait_for_timeout(200)
        self.page.keyboard.press("ArrowUp")
        self.page.wait_for_timeout(300)
        self.page.keyboard.press("Space")

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
