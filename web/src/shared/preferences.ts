import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { useSyncExternalStore } from "react";

export type Language = "zh" | "en";
export type Theme = "light" | "dark" | "system";

const LANGUAGE_KEY = "cycledock.language";
const THEME_KEY = "cycledock.theme";

function getStoredLanguage(): Language {
  try {
    const value = localStorage.getItem(LANGUAGE_KEY);
    return value === "en" ? "en" : "zh";
  } catch {
    return "zh";
  }
}

function getStoredTheme(): Theme {
  try {
    const value = localStorage.getItem(THEME_KEY);
    return value === "dark" || value === "system" ? value : "light";
  } catch {
    return "light";
  }
}

let theme: Theme = getStoredTheme();
const listeners = new Set<() => void>();
let media: MediaQueryList | undefined;

// 主题是本设备的界面偏好，不属于 Workspace，也不进入服务端业务模型。
function applyTheme() {
  const dark = theme === "dark" || (theme === "system" && media?.matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
  const themeColor = dark ? "#15202e" : "#ffffff";
  document
    .querySelectorAll('meta[name="theme-color"]')
    .forEach((meta) => meta.setAttribute("content", themeColor));
}

export function initializePreferences() {
  media = window.matchMedia("(prefers-color-scheme: dark)");
  media.addEventListener("change", applyTheme);
  window.addEventListener("storage", (event) => {
    if (event.key === THEME_KEY) {
      theme = getStoredTheme();
      applyTheme();
      listeners.forEach((listener) => listener());
    }
    if (event.key === LANGUAGE_KEY) {
      const language = getStoredLanguage();
      void i18n.changeLanguage(language);
      document.documentElement.lang = language === "en" ? "en" : "zh-CN";
    }
  });
  applyTheme();
}

export function setTheme(next: Theme) {
  theme = next;
  try {
    localStorage.setItem(THEME_KEY, next);
  } catch {
    /* browsing without storage */
  }
  applyTheme();
  listeners.forEach((listener) => listener());
}
export function useTheme(): [Theme, (next: Theme) => void] {
  const current = useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () => theme,
    () => theme,
  );
  return [current, setTheme];
}

const en = {
  不指定设备: "No device preference",
  "选择仅作用于之后发送的消息。":
    "Your selection applies to future messages only.",
  "所选设备当前不可用，可以切换或取消指定。":
    "The selected device is unavailable. Choose another or clear the selection.",
  设备在线列表: "Device status",
  设备: "Devices",
  "NexusDock 设备": "NexusDock devices",
  "{{online}} / {{total}} 在线": "{{online}} / {{total}} online",
  "正在读取设备…": "Loading devices…",
  "设备授权已过期，请重新连接。":
    "Device authorization has expired. Please reconnect.",
  "设备状态暂时不可用，请重试。":
    "Device status is temporarily unavailable. Please retry.",
  "当前部署尚未启用 NexusDock 设备连接。":
    "NexusDock device connections are not enabled on this deployment.",
  "连接 NexusDock，查看设备在线状态。":
    "Connect NexusDock to view device status.",
  "请联系工作空间所有者连接 NexusDock。":
    "Ask your workspace owner to connect NexusDock.",
  "此授权账号下暂无设备。":
    "There are no devices under this authorized account.",
  设备列表: "Device list",
  系统未知: "Unknown system",
  在线: "Online",
  离线: "Offline",
  刷新设备状态: "Refresh devices",
  连接设备: "Connect devices",
  设备连接设置: "Device connection settings",
  开启新消息通知: "Enable message notifications",
  "离开卡片或切到后台，有新消息时通知。":
    "Get notified of new messages when viewing another card or working in the background.",
  暂不开启: "Not now",
  浏览器通知: "Browser notifications",
  已开启: "On",
  未开启: "Off",
  开启通知: "Enable notifications",
  关闭通知: "Disable notifications",
  卡片有更新: "Card updated",
  新消息: "New message",
  讨论: "Discussion",
  主聊天: "Main chat",
  主讨论始终置顶: "Main discussion is always pinned",
  打开主聊天: "Open main chat",
  关闭主聊天: "Close main chat",
  在完整页面打开: "Open full page",
  打开这张卡: "Open this card",
  "卡片有更新，点击查看": "Card updated. Click to view.",
  浏览器通知启动失败: "Failed to start browser notifications",
  "切到后台或离开当前卡片后，有新消息时通知。网页需保持打开。":
    "Notify on new messages when in the background or viewing another card. Keep the page open.",
  "切到后台、离开当前卡片，或关闭页面后，有新消息时通知。":
    "Notify on new messages in the background, on another card, or after the page is closed.",
  "iPhone 和 iPad 需要先通过分享菜单把 CycleDock 添加到主屏幕，再用主屏幕图标打开并开启通知。Safari 标签页不能接收推送。":
    "On iPhone and iPad, add CycleDock to the Home Screen from the share menu, then open that icon and enable notifications. A Safari tab cannot receive push.",
  "关闭页面后的推送尚未配置，当前只在网页打开时通知。":
    "Push after the page is closed is not configured. Notifications work only while the page is open.",
  "当前环境不支持浏览器通知，请使用 HTTPS 或本机浏览器。":
    "Notifications are unavailable. Use HTTPS or localhost in a supported browser.",
  "通知权限已被阻止，请在浏览器的网站设置中允许通知。":
    "Notifications are blocked. Allow them in your browser's site settings.",
  聊天: "Chat",
  看板: "Board",
  工作: "Work",
  任务: "Tasks",
  账户与工作空间: "Account & workspace",
  已切换工作空间: "Workspace switched",
  工作空间切换失败: "Failed to switch workspace",
  工作空间加载失败: "Failed to load workspaces",
  设置: "Settings",
  "管理个人偏好、模型服务与外部连接。":
    "Manage personal preferences, model services, and external connections.",
  导航抽屉: "Navigation menu",
  退出登录: "Sign out",
  工作空间: "Workspace",
  常规: "General",
  账户: "Account",
  模型服务: "Model providers",
  "Agent 设置": "Agent settings",
  "MCP 服务": "MCP services",
  "AI 客户端": "AI clients",
  "无法确认模型配置，请重试。":
    "Could not check model configuration. Please retry.",
  "正在检查模型配置…": "Checking model configuration…",
  "尚未配置可用模型，Agent 暂时无法回复。草稿会保留，已排队的消息将在配置完成后继续执行。":
    "No model is enabled, so the Agent cannot reply yet. Your draft is preserved. Queued messages will run once a model is configured.",
  配置模型: "Configure model",
  "请联系工作区所有者配置并启用模型。":
    "Ask the workspace owner to configure and enable a model.",
  项目设置: "Project settings",
  "修改当前项目的名称与项目说明。":
    "Edit this project's name and project details.",
  个人: "Personal",
  "AI 配置": "AI configuration",
  外部连接: "External connections",
  显示与偏好: "Display & preferences",
  界面语言: "Language",
  外观: "Appearance",
  简体中文: "Simplified Chinese",
  英语: "English",
  浅色: "Light",
  深色: "Dark",
  跟随系统: "System",
  "选择界面语言与颜色模式，偏好仅保存在当前浏览器。":
    "Choose your language and appearance. Preferences are saved in this browser.",
  显示名称: "Display name",
  邮箱: "Email",
  当前账号: "Current account",
  "查看当前登录邮箱，或安全退出 CycleDock。":
    "View the signed-in email address or safely sign out of CycleDock.",
  账号与工作空间: "Account & workspace",
  未提供: "Not provided",
  "Hosted 身份在 NexusDock 管理；模型授权不属于登录身份。":
    "Manage your hosted identity in NexusDock; model authorization is separate.",
  需要所有者权限: "Owner access required",
  "这些设置只允许 Workspace owner 修改。":
    "Only workspace owners can change these settings.",
  "已授权的 AI 客户端": "Authorized AI clients",
  "这些 AI 客户端已通过 CycleDock OAuth 获得当前工作空间的 MCP 访问权限。":
    "These AI clients have access to this workspace's MCP tools through CycleDock OAuth.",
  "已授权访问 CycleDock MCP": "Authorized for CycleDock MCP",
  "还没有已授权的 AI 客户端。完成 MCP OAuth 授权后会显示在这里。":
    "No AI clients are authorized yet. Clients will appear here after MCP OAuth authorization.",
  未找到设置页面: "Settings page not found",
  "请选择左侧的设置分类。": "Choose a category from the navigation.",
  "没有项目，请先创建项目。": "No projects yet. Create one first.",
  选择项目: "Select project",
  名称: "Name",
  描述: "Description",
  项目说明: "Project details",
  "介绍项目背景、目标，以及希望 Agent 遵守的工作要求。对项目内所有 Card 生效。":
    "Describe the project background, goals, and requirements for the Agent. Applies to all Cards in this project.",
  保存项目: "Save project",
  项目已保存: "Project saved",
  "从一个想法开始，与 Agent 讨论，再把方向变成任务。":
    "Start with an idea, discuss it with an Agent, and turn it into actionable work.",
  新建讨论: "New discussion",
  讨论列表: "Discussions",
  没有匹配的讨论: "No matching discussions",
  打开讨论列表: "Open discussions",
  工作列表: "Work list",
  打开工作列表: "Open work list",
  任务列表: "Task list",
  打开任务列表: "Open task list",
  定时列表: "Schedule list",
  打开定时列表: "Open schedule list",
  "今天想推进什么？": "What would you like to move forward today?",
  "发送消息，开始讨论…": "Send a message to start a discussion…",
  还没有项目: "No projects yet",
  "请先在看板中创建一个项目。": "Create a project from the Board first.",
  "请先在工作区中创建一个项目。": "Create a project from Work first.",
  前往工作: "Go to Work",
  前往任务: "Go to Tasks",
  "读取项目…": "Loading projects…",
  项目: "Project",
  默认项目: "Default Project",
  "搜索讨论…": "Search discussions…",
  "读取讨论…": "Loading discussions…",
  还没有讨论: "No discussions yet",
  "新建一个讨论，直接发送第一条消息。":
    "Create a discussion and send your first message.",
  开始讨论: "Start a discussion",
  新讨论: "New discussion",
  "继续对话，探索想法…": "Continue the conversation…",
  已归档: "Archived",
  讨论中: "Active",
  "还没有项目，请先在看板中创建一个项目。":
    "No projects yet. Create one from the Board first.",
  前往看板: "Go to Board",
  创建讨论失败: "Failed to create discussion",
  "把讨论变成工作。": "Turn discussions into work.",
  "把讨论变成任务。": "Turn discussions into tasks.",
  "让进展清晰可见。": "Make progress visible.",
  "统一的对话时间线、看板、持续工作与 AI Agent。":
    "A shared conversation timeline, boards, ongoing work and AI Agents.",
  "统一的对话时间线、看板、持续任务与 AI Agent。":
    "A shared conversation timeline, boards, ongoing tasks and AI Agents.",
  工作空间权限由服务端维护: "Workspace permissions are enforced by the server",
  "登录 CycleDock": "Sign in to CycleDock",
  跨卡片委派: "Delegated",
  继续进入你的工作空间: "Continue to your workspace",
  使用: "Continue with",
  登录: "Sign in",
  管理员密码: "Administrator password",
  输入管理员密码: "Enter administrator password",
  登录失败: "Sign-in failed",
  "Hosted 使用 NexusDock 统一认证；Self-hosted 可使用管理员密码。":
    "Hosted uses NexusDock SSO; self-hosted can use an administrator password.",
  "正在验证会话…": "Verifying session…",
  "正在加载会话…": "Loading conversation…",
  待办: "To do",
  进行中: "Working",
  待审核: "In review",
  待验收: "Pending acceptance",
  定时任务: "Scheduled tasks",
  定时: "Schedules",
  已完成: "Done",
  已关闭: "Closed",
  "PROJECT BOARD": "PROJECT BOARD",
  DISCUSSIONS: "DISCUSSIONS",
  "查看执行状态，跟踪长期工作。": "Track tasks, progress and ongoing work.",
  新建项目: "New project",
  "创建一个新的项目看板来组织任务与 Agent 会话。":
    "Create a new project board to organize tasks and Agent sessions.",
  输入项目名称: "Enter project name",
  描述该项目的工作范围或目标: "Describe the project scope or goals",
  新建待办: "New to-do",
  "添加一个待办任务到当前项目看板中。": "Add a to-do task to this board.",
  取消: "Cancel",
  待办标题: "To-do title",
  待办描述: "To-do description",
  "要完成什么？": "What needs to be done?",
  "补充目标、范围或完成条件": "Add goals, scope, or completion criteria",
  创建待办: "Create to-do",
  切换项目: "Switch project",
  从讨论中生成工作: "Generate work from discussions",
  没有任务: "No tasks",
  计划: "Plans",
  计划列表: "Plan list",
  "Agent 列表": "Agent list",
  活动列表: "Activity list",
  全部来源: "All sources",
  打开计划列表: "Open plan list",
  全部计划: "All plans",
  未命名计划: "Untitled plan",
  已暂停: "Paused",
  健康状态: "Health",
  正常: "Healthy",
  需留意: "Needs attention",
  异常: "Critical",
  已确认: "Acknowledged",
  已标记为已确认: "Marked as acknowledged",
  巡检已确认: "Check-in acknowledged",
  "巡检状态：需留意": "Check-in: Needs attention",
  "巡检状态：异常": "Check-in: Critical",
  待首次巡检: "Awaiting first check-in",
  最近巡检: "Last check-in",
  已记录: "Recorded",
  已替代: "Superseded",
  已超时: "Timed out",
  已取消: "Cancelled",
  日程已停用: "Schedule disabled",
  未设置自动执行时间: "No automatic schedule",
  "管理长期工作，查看执行结果与自动执行日程。":
    "Manage ongoing work, results and automatic schedules.",
  "查看任务执行状态，跟踪待办进度。": "Track task status and to-do progress.",
  这个项目还没有计划: "No plans in this project yet",
  这个筛选下没有计划: "No plans match this filter",
  "从讨论中生成长期工作，在详情中配置自动执行时间。":
    "Generate ongoing work from a discussion and configure its schedule in the details.",
  未命名任务: "Untitled task",
  该状态暂无任务: "No tasks in this state",
  未运行: "Not running",
  项目名称: "Project name",
  项目描述: "Project description",
  创建项目: "Create project",
  移动失败: "Could not move card",
  任务详情: "Task details",
  任务信息: "Task info",
  讨论信息: "Discussion info",
  切换任务状态: "Change task status",
  状态更新失败: "Failed to update status",
  请先提交待审核: "Move to review first",
  请先提交待验收: "Move to acceptance first",
  返回: "Back",
  生成任务: "Generate task",
  正在执行: "Running",
  正在操作: "Operating",
  任务进度: "Task progress",
  关闭任务进度: "Close task progress",
  当前步骤: "Current step",
  已阻塞: "Blocked",
  未开始: "Not started",
  已通过: "Passed",
  未通过: "Failed",
  排队中: "Queued",
  运行中: "Running",
  发送: "Send",
  加入队列: "Queue",
  已收到回复: "Reply received",
  执行完成: "Completed",
  "正在连接实时通道…": "Connecting to live updates…",
  "此讨论已归档，不能继续发送消息": "This discussion is archived",
  讨论已归档: "Archived discussion",
  "回复 Agent，继续执行…": "Reply to the Agent to continue…",
  "发送消息，继续推进任务…": "Send a message to keep work moving…",
  执行失败: "Run failed",
  生成任务指令已提交: "Task generation requested",
  从第一句话开始: "Start with your first message",
  开始协作: "Start collaborating",
  暂无卡片详细描述: "No description provided for this card",
  "你的消息、Agent 结果和任务状态都会出现在这里。":
    "Your messages, Agent results and status updates appear here.",
  跳到最新: "Jump to latest",
  正在生成回复: "Generating a reply",
  阶段进度: "Progress",
  巡检: "Check-in",
  状态变化: "Status update",
  提交审核: "Submitted for review",
  提交验收: "Submitted for acceptance",
  通过审核: "Approve",
  通过验收: "Accept",
  已通过审核: "Approved",
  已通过验收: "Accepted",
  驳回: "Rejected",
  发现: "Finding",
  结果: "Result",
  "NexusDock 远程设备": "NexusDock remote devices",
  远程设备: "Remote devices",
  "连接并管理你的远程设备，让 Agent 在授权范围内调用设备协助完成工作。":
    "Connect and manage your remote devices so Agents can use them to help with your work.",
  连接你的远程设备: "Connect your remote devices",
  "连接后，Agent 可在你的授权下使用该设备上的技能、工具与本地环境协助完成任务。":
    "Once connected, Agents can use the skills, tools, and local environment on your device within your permission.",
  立即连接设备: "Connect device",
  "连接后可随时在此查看设备状态，或断开连接。":
    "You can view device status or disconnect at any time.",
  "正在检查远程设备连接状态…": "Checking remote device connection…",
  "正在连接远程设备并拉取能力快照…":
    "Connecting to remote devices and loading capabilities…",
  设备安全守卫: "Device tool guard",
  守卫已关闭: "Guard disabled",
  仅拦截高危: "Block high-risk",
  严格模式: "Strict mode",
  独立配置: "Configure",
  收起配置: "Collapse",
  重置为默认: "Reset to default",
  仅所有者可修改: "Owner only",
  全局规则设置: "Global guard settings",
  切换设备安全守卫: "Toggle device tool guard",
  "已为该设备开启工具安全守卫": "Device tool guard enabled",
  "已关闭该设备工具安全守卫": "Device tool guard disabled",
  "已启用工作区全局安全守卫策略，调用高危或敏感工具前自动拦截评估。":
    "Protected by the workspace global tool guard policy before high-risk or sensitive calls.",
  "未开启安全守卫，该设备工具调用将按默认放行策略执行。":
    "Tool guard is disabled for this device; tool calls follow default execution.",
  工具守卫: "Tool Guard",
  工作区全局工具守卫: "Workspace Global Tool Guard",
  "统一配置所有受保护远程设备与 MCP 服务的风险评估档位、评估小模型、超时时间与全局规则。":
    "Configure unified guard mode, risk evaluation model, timeout, and custom rules shared across all guarded remote devices and MCP services.",
  "各远程设备与 MCP 服务是否开启守卫可在对应页面单独开关；开启后将统一按此处配置执行评估。":
    "Enable or disable tool guard per remote device or MCP service on their respective pages; enabled targets share this workspace policy.",
  管理设备开关: "Manage device switches",
  "管理 MCP 开关": "Manage MCP switches",
  全局拦截档位: "Global guard mode",
  风险评估小模型: "Risk evaluation model",
  已失效或停用的路由: "Disabled or removed route",
  "当前绑定的评估模型路由已被停用或删除，运行时会自动回退到首个可用模型路由；建议重新选择并保存。":
    "The bound evaluation model route is disabled or removed. Runtime falls back to the first enabled route; consider selecting a new route.",
  "允许范围 2~60 秒，超时后安全兜底挂起确认":
    "Allowed range 2–60s; falls back to human confirmation on timeout",
  全局自定义守卫规则: "Global custom guard rules",
  "这些规则会作为最高优先级补充约束传入风险评估小模型，同时适用于所有开启守卫的远程设备与 MCP 服务。":
    "Passed to the risk evaluation model as top-priority rules for all guarded remote devices and MCP services.",
  "例如：允许在独立 Git worktree 中自由修改测试代码；涉及重启生产容器、删除日志或修改线上配置必须触发人工确认。":
    "Example: Allow modifying test files in an isolated Git worktree; require human approval for restarting production containers, deleting logs, or editing production configs.",
  保存全局守卫配置: "Save global guard settings",
  全局工具守卫配置已保存: "Global tool guard settings saved",
  "针对此设备节点独立配置工具调用安全拦截策略、评估模型与自定义规则。":
    "Configure tool safety policy, evaluation model, and custom rules for this device.",
  拦截模式: "Guard mode",
  关闭拦截: "Disabled",
  "仅拦截高危不可逆操作（推荐）":
    "Block high-risk irreversible operations (recommended)",
  "严格模式（拦截所有写操作）": "Strict mode (block all write operations)",
  风险评估轻量小模型: "Risk evaluation model",
  默认使用首个可用路由: "Use first enabled route",
  "评估超时（秒）": "Evaluation timeout (s)",
  此设备专属自定义守卫规则: "Custom guard rules for this device",
  "声明此设备特定目录、容器或环境的放行或严禁要求":
    "Specify allowed or forbidden directories, containers, or operations on this device",
  保存设备守卫规则: "Save guard rules",
  设备安全守卫配置已保存: "Device guard settings saved",
  已重置为默认策略: "Reset to default guard policy",
  已允许执行: "Approved",
  已拒绝执行: "Rejected",
  收起详情: "Collapse details",
  模型提供商: "Model provider",
  "内置 Agent": "Built-in Agent",
  主导航: "Main navigation",
  发送失败: "Failed to send",
  提交失败: "Failed to submit",
  取消失败: "Failed to cancel",
  取消排队失败: "Failed to cancel queued work",
  重试失败: "Retry failed",
  取消排队: "Cancel queued work",
  重试: "Retry",
  "系统触发 / 未关联历史": "System-triggered / unlinked history",
  待执行队列: "Pending queue",
  引导: "Steer",
  引导失败: "Could not steer queued input",
  优先等待执行: "Waiting with priority",
  "已优先排队，等待可用执行名额；仍可编辑或取消":
    "Queued with priority, waiting for execution capacity. You can still edit or cancel.",
  "立即执行这条排队输入，替代当前执行":
    "Run this queued input next, replacing the current execution",
  排队输入: "Queued input",
  编辑内容: "Edit content",
  "新讨论发送失败，草稿已保留":
    "Failed to send the new discussion. Draft saved.",
  "重试失败，草稿仍已保留": "Retry failed. Draft remains saved.",
  打开讨论: "Open discussion",
  重试发送: "Retry send",
  中止: "Stop",
  统一消息时间线: "Conversation timeline",
  复制代码: "Copy code",
  "Agent 正在工作": "Agent is working",
  "保存重要事件与结果，不保存模型内部思维链或原始工具记录。":
    "CycleDock keeps key events and results, not private reasoning or raw tool traces.",
  "Agent 配置已保存": "Agent settings saved",
  "这些配置作用于整个 Workspace；具体模型连接请前往「模型服务」。":
    "These settings apply to your workspace; manage providers under Model providers.",
  "整个工作空间的默认指令，与单个项目的指令分开保存。":
    "Workspace-wide instructions; project instructions are saved separately.",
  "保存 Agent 设置": "Save Agent settings",
  编辑: "Edit",
  编辑任务: "Edit task",
  编辑讨论: "Edit discussion",
  编辑定时任务: "Edit scheduled task",
  已保存: "Saved",
  保存失败: "Failed to save",
  "保存中…": "Saving…",
  讨论标题: "Discussion title",
  任务标题: "Task title",
  定时任务标题: "Scheduled task title",
  焦点: "Focus",
  当前焦点: "In focus",
  待确认: "Needs approval",
  "{{count}} 待确认": "{{count}} awaiting approval",
  "{{count}} 运行中": "{{count}} running",
  "{{count}} 未读": "{{count}} unread",
  "{{count}} 待验收": "{{count}} in review",
  "全部 ({{count}})": "All ({{count}})",
  "查看全部 {{count}} 个焦点事项": "View all {{count}} focus items",
  "当前没有待确认、正在运行、未读或待处理的事项":
    "No items awaiting approval, running, unread, or pending right now",
  "搜索卡片标题或描述...": "Search card title or description...",
  清空搜索: "Clear search",
  未找到匹配的卡片: "No matching cards found",
  返回上级: "Back",
  "本卡片允许此设备": "Allow this device on card",
  "本卡片允许此 MCP": "Allow this MCP on card",
  "本卡片允许 {{target}}": "Allow {{target}} on card",
  "设备 · {{target}}": "Device · {{target}}",
  "MCP · {{target}}": "MCP · {{target}}",
} as const;

void i18n.use(initReactI18next).init({
  lng: getStoredLanguage(),
  fallbackLng: "zh",
  resources: {
    zh: { translation: {} },
    en: { translation: en },
  },
  interpolation: { escapeValue: false },
  initAsync: false,
});

export function setLanguage(language: Language) {
  try {
    localStorage.setItem(LANGUAGE_KEY, language);
  } catch {
    /* browsing without storage */
  }
  void i18n.changeLanguage(language);
  document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
}

export function initializeLanguage() {
  setLanguage(getStoredLanguage());
}

export { i18n };
