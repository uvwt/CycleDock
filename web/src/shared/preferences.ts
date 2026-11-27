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
  创建并开始执行: "Create & start",
  创建后立即开始执行: "Start immediately after creating",
  "创建后立即交给 Cycle 开始推进此待办。":
    "Hand this to-do to Cycle right after creation.",
  "仅先收入待办池留档，不消耗 Token，可随时在卡片内一键启动。":
    "Save to the backlog without using tokens; start with one click anytime.",
  开始执行: "Start execution",
  "正在启动...": "Starting...",
  已开始执行: "Execution started",
  启动失败: "Failed to start",
  "当前待办尚未开始执行，点击即可由 Cycle 开始处理，或直接发送补充说明。":
    "This to-do hasn't started yet. Click to let Cycle begin, or send additional context below.",
  "当前定时任务尚未开始执行，点击即可立即启动首轮执行，或直接发送补充说明。":
    "This scheduled task hasn't run yet. Click to start the first run, or send additional context below.",
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
  服务重启续跑: "Resuming after restart",
  已由新消息替代: "Replaced by a newer message",
  已停止生成: "Generation stopped",
  等待执行: "Queued",
  执行已过期: "Run expired",
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
  展开巡检详情: "Expand check-in details",
  收起巡检详情: "Collapse check-in details",
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
  "已失效（已有后续推进）": "Superseded (newer activity)",
  "工具调用安全守卫 · 已失效（已有后续推进）":
    "Tool guard · Superseded (newer activity)",
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
  "该讨论卡由远程通道自动托管，标题与描述随通道配置同步，无法在此编辑。如需改名，请前往「设置 → 远程通道」。":
    "This discussion card is managed by a remote channel. Its title and description stay synced with the channel settings and cannot be edited here. Rename it under Settings → Remote Channels.",
  任务标题: "Task title",
  定时任务标题: "Scheduled task title",
  焦点: "Focus",
  当前焦点: "In focus",
  需要关注: "Needs attention",
  置顶常驻: "Pinned",
  待确认: "Needs approval",
  "{{count}} 待确认": "{{count}} awaiting approval",
  "{{count}} 异常": "{{count}} critical",
  "{{count}} 需留意": "{{count}} needs attention",
  "{{count}} 运行中": "{{count}} running",
  "{{count}} 未读": "{{count}} unread",
  "{{count}} 待验收": "{{count}} in review",
  "{{count}} 置顶": "{{count}} pinned",
  "{{count}} 个焦点事项": "{{count}} focus items",
  "全部 ({{count}})": "All ({{count}})",
  "查看全部 {{count}} 个焦点事项": "View all {{count}} focus items",
  "当前没有待确认、正在运行、未读、待处理或置顶的事项":
    "No items awaiting approval, running, unread, pending, or pinned right now",
  "搜索卡片标题或描述...": "Search card title or description...",
  清空搜索: "Clear search",
  未找到匹配的卡片: "No matching cards found",
  返回上级: "Back",
  "本卡片允许此设备": "Allow this device on card",
  "本卡片允许此 MCP": "Allow this MCP on card",
  "本卡片允许 {{target}}": "Allow {{target}} on card",
  "设备 · {{target}}": "Device · {{target}}",
  "MCP · {{target}}": "MCP · {{target}}",
  静音通知: "Mute notifications",
  取消静音: "Unmute notifications",
  已静音通知: "Notifications muted",
  已恢复通知: "Notifications unmuted",
  通知已静音: "Notifications muted",
  通知已开启: "Notifications enabled",
  推送通知: "Notifications",
  接收新消息推送: "Push notifications on",
  静音设置失败: "Failed to update notification mute",
  "搜索设备、插件、Skill 或 MCP…": "Search devices, plugins, skills, or MCP…",
  插件: "Plugin",
  已装插件: "Plugins",
  "{{count}} 个插件": "{{count}} plugins",
  插件提供: "From plugins",
  "内置 / 托管": "Built-in / Managed",
  共享技能: "Shared",
  独立服务: "Standalone",
  插件技能: "Plugin skill",
  "插件 · {{name}}": "Plugin · {{name}}",
  未命名插件: "Unnamed plugin",
  插件已停用: "Disabled",
  独立与共享技能: "Built-in & shared skills",
  "独立 MCP 服务": "Standalone MCP servers",
  当前筛选下暂无匹配的技能: "No skills match this filter",
  "当前筛选下暂无匹配的 MCP 服务": "No MCP servers match this filter",
  "展开其余 {{count}} 项": "Show {{count}} more",
  "单条消息最多发送 {{max}} 张图片": "Up to {{max}} images per message",
  "已跳过不支持的图片格式，请上传 PNG / JPG / WebP / GIF":
    "Unsupported image format skipped. Use PNG, JPG, WebP, or GIF",
  "图片超过 20 MB 上限，无法上传": "Image exceeds the 20 MB upload limit",
  "图片上传失败": "Failed to upload image",
  "上传失败": "Upload failed",
  "移除图片": "Remove image",
  "图片已过期": "Image expired",
  "文件已过期": "File expired",
  "图片": "Image",
  "大图预览": "Image preview",
  "添加图片（支持粘贴或拖拽）": "Add images (paste or drag & drop)",
  "添加图片或文本/代码文件（支持粘贴或拖拽）":
    "Add images or plain text/code files (paste or drag & drop)",
  "添加图片": "Add images",
  "添加附件": "Add attachments",
  "添加图片 / 文件": "Add images / files",
  "移除附件": "Remove attachment",
  "附带图片（可选）": "Attachments (optional)",
  "附带附件（可选）": "Attachments (optional)",
  "附带附件（{{count}}）": "Attachments ({{count}})",
  "支持点击上传、拖拽或粘贴图片，最多 {{max}} 张。":
    "Click, drag & drop, or paste up to {{max}} images.",
  "支持图片及纯文本/日志/代码文件（单文件 ≤ 2 MB），支持点击、拖拽或粘贴，最多 {{max}} 个。":
    "Supports images and plain text/log/code files (≤ 2 MB each). Click, drag & drop, or paste up to {{max}} files.",
  "图片上传中...": "Uploading images...",
  "附件上传中...": "Uploading attachments...",
  "文本文件": "Text file",
  "{{count}} 行": "{{count}} lines",
  "下载文件": "Download file",
  "下载": "Download",
  "复制": "Copy",
  "已复制": "Copied",
  "复制失败": "Copy failed",
  "正在加载文件...": "Loading file...",
  "加载文件内容失败": "Failed to load file content",
  "上传中...": "Uploading...",
  "附带图片（{{count}}）": "Attached images ({{count}})",
  "附带 {{count}} 张图片": "{{count}} attached images",
  "附带 {{count}} 个附件": "{{count}} attachments",
  "点击查看大图": "Click to view full size",
  "打开原图": "Open original",
  "图片加载失败": "Failed to load image",
  "消息图片": "Message image",
  "图片大图预览": "Full-size image preview",
  "关闭预览": "Close preview",
  "消息图片大图": "Full-size message image",
  收起其余项: "Show less",
  加载更多插件分组: "Load more plugin groups",
  收起多余插件分组: "Collapse extra plugin groups",
  回答模型: "Response model",
  请求模型: "Requested model",
  "上游未返回，按请求模型展示": "Not returned by upstream; showing requested model",
  模型提供方: "Model provider",
  重新回答: "Regenerate",
  切换模型或重新回答: "Regenerate or switch model",
  "正在重新生成...": "Regenerating...",
  "当前正在生成回答，请稍候重试": "Response is currently generating; please wait",
  重新回答失败: "Failed to regenerate response",
  选择模型重新回答: "Choose a model to regenerate",
  "仅对本次重试生效，不会修改当前卡片默认模型。":
    "Applies to this retry only and does not change the card default model.",
  "仅对本次重试生效，确认后将重新生成该轮并清空后续消息。":
    "Applies to this retry only; confirming will regenerate this turn and clear following messages.",
  使用当前默认重试: "Retry with current default",
  "上次使用：{{model}}": "Last used: {{model}}",
  切换指定模型: "Switch model",
  上次模型: "Last used",
  "确认重新生成回答？": "Regenerate this response?",
  "将使用 {{model}} 重新生成该轮回答，并清空此消息之后的后续对话记录。":
    "This will regenerate the response using {{model}} and clear all messages after it.",
  "将重新生成该轮回答，并清空此消息之后的后续对话记录。":
    "This will regenerate the response and clear all messages after it.",
  触发器: "Triggers",
  触发器与等待: "Triggers & Watchers",
  "配置 GitHub / CircleCI / 通用 Webhook 触发器，排查投递日志，并管理各卡片挂起的事件等待与定时自唤醒。":
    "Configure GitHub, CircleCI, and generic Webhook triggers, inspect delivery logs, and manage pending event watchers or delayed wakeups.",
  新建触发器: "New trigger",
  "Webhook 触发器": "Webhook triggers",
  挂起等待与唤醒: "Pending watchers",
  投递日志: "Deliveries",
  "加载中…": "Loading…",
  "尚未配置任何 Webhook 触发器": "No Webhook triggers configured yet",
  "接入 GitHub Actions、CircleCI 或自定义 Webhook，可在外部构建完成或告警到达时自动唤醒对应卡片。":
    "Connect GitHub Actions, CircleCI, or custom webhooks to automatically wake cards when external builds or alerts finish.",
  创建第一个触发器: "Create your first trigger",
  已启用: "Enabled",
  已停用: "Disabled",
  最近: "Latest",
  已命中: "Triggered",
  已过滤: "Filtered",
  未命中: "Ignored",
  冷却跳过: "Cooldown",
  验签拒绝: "Rejected",
  路由动作: "Routing",
  唤醒挂起等待的卡片: "Wake matching card watchers",
  触发指定卡片: "Trigger a specific card",
  "自动创建 Todo 卡片": "Create a Todo card",
  目标项目: "Target project",
  调度模式: "Dispatch",
  排队执行: "Enqueue",
  打断执行: "Interrupt",
  冷却: "Cooldown",
  启用触发器: "Toggle trigger",
  "已复制 Webhook URL": "Webhook URL copied",
  "已复制 Webhook 密钥": "Webhook secret copied",
  "已复制 Match Key": "Match key copied",
  "复制失败，请手动复制": "Copy failed, please copy manually",
  密钥指纹: "Secret hint",
  "历史明文密钥不可回看，如需更换请轮换密钥":
    "Plaintext secret is hidden after creation; rotate to issue a new one",
  最近触发: "Last activity",
  事件: "Events",
  动作: "Actions",
  结论: "Conclusions",
  分支: "Branches",
  仓库: "Repos",
  轮换密钥: "Rotate secret",
  查看日志: "Deliveries",
  全部项目: "All projects",
  "仅显示等待中 (Armed)": "Armed only",
  刷新: "Refresh",
  当前没有挂起中的等待或定时唤醒: "No armed watchers or wakeups",
  暂无等待与唤醒记录: "No watcher records yet",
  "当 Agent 注册一次性事件等待（如等待 PR CI 完成）或设置延时自唤醒时，会实时汇总显示在这里。":
    "One-shot event watchers and delayed self-wakeups registered by Agents will appear here.",
  事件等待: "Event watcher",
  定时自唤醒: "Delayed wakeup",
  等待中: "Armed",
  已唤醒: "Triggered",
  已超时兜底: "Timed out",
  "取消中…": "Cancelling…",
  取消等待: "Cancel",
  超时兜底: "Timeout fallback",
  唤醒时间: "Wakeup at",
  结束于: "Resolved at",
  全部触发器: "All triggers",
  "暂无 Webhook 投递记录": "No Webhook deliveries yet",
  "外部平台向 Webhook Endpoint 推送事件后，这里会记录验签结果、Match Key 和触发详情。":
    "Once external systems post to your Webhook endpoint, signature results, match keys, and linked cards will appear here.",
  原因: "Reason",
  关联卡片: "Linked cards",
  "请立即保存 Webhook 密钥": "Save your Webhook secret now",
  "出于安全考虑，明文密钥仅在本次创建或轮换时展示一次，关闭此窗口后将无法再次查看。":
    "For security, the plaintext secret is shown only once upon creation or rotation. You will not be able to view it again after closing.",
  "Webhook Secret（仅展示一次）": "Webhook Secret (shown once)",
  "请在外部请求头中携带 Authorization: Bearer <Secret>。":
    "Send Authorization: Bearer <Secret> in the incoming request headers.",
  "请在 GitHub Webhook 设置中将 Secret 填入对应栏位，签名头为 X-Hub-Signature-256。":
    "Paste this secret into GitHub Webhook settings (signed via X-Hub-Signature-256).",
  "请在 CircleCI Webhook 设置中填入该 Secret，签名头为 circleci-signature。":
    "Paste this secret into CircleCI Webhook settings (signed via circleci-signature).",
  "请使用 HMAC-SHA256 对原始请求体签名，并通过请求头 {{header}} 发送。":
    "Sign the raw request body with HMAC-SHA256 and send it in the {{header}} header.",
  "我已保存，关闭": "I have saved it, close",
  "轮换 Webhook 密钥？": "Rotate Webhook secret?",
  "轮换后旧密钥会立即失效，外部平台（GitHub / CircleCI 等）必须同步更新为新密钥才能继续通过验签。":
    "The previous secret stops working immediately. Update your external webhook provider with the new secret.",
  "轮换中…": "Rotating…",
  确认轮换: "Rotate secret",
  "删除此触发器？": "Delete this trigger?",
  "删除「{{name}}」后，该 Webhook Endpoint 将立刻停止接收事件，关联的投递历史也会一并清除。":
    "Deleting \"{{name}}\" immediately disables its webhook endpoint and removes its delivery history.",
  "删除中…": "Deleting…",
  确认删除: "Delete",
  删除: "Delete",
  "编辑 Webhook 触发器": "Edit Webhook trigger",
  "新建 Webhook 触发器": "New Webhook trigger",
  "调整路由动作、过滤条件或启停状态。来源类型与认证模式创建后固定。":
    "Adjust routing, filters, or enabled state. Provider and auth mode are fixed after creation.",
  "创建后将自动生成专属 Webhook URL 与一次性密钥，可用于 GitHub、CircleCI 或任意外部系统。":
    "Generates a dedicated Webhook URL and one-time secret for GitHub, CircleCI, or any custom system.",
  触发器名称: "Trigger name",
  "例如：GitHub CI 完成唤醒": "e.g. GitHub CI completion wakeup",
  来源类型: "Provider",
  认证方式: "Auth mode",
  签名请求头: "Signature header",
  "自定义密钥（可选）": "Custom secret (optional)",
  "留空将自动生成高强度随机密钥，仅创建时展示一次":
    "Leave blank to auto-generate a strong random secret (shown once)",
  "留空自动生成（至少 16 位）": "Leave blank to auto-generate (min 16 chars)",
  路由与执行动作: "Routing & dispatch",
  命中后的路由方式: "Routing mode",
  "排队执行 (enqueue)": "Enqueue",
  "打断当前执行 (interrupt)": "Interrupt current run",
  所属项目: "Project",
  请选择项目: "Select a project",
  目标卡片: "Target card",
  "加载卡片…": "Loading cards…",
  请选择卡片: "Select a card",
  "冷却时间（秒）": "Cooldown (seconds)",
  "0 表示不限制，防止高频抖动重复唤醒":
    "0 disables cooldown; prevents repeated wakeups from noisy events",
  创建后立即启用: "Enabled",
  "过滤与匹配条件（可选）": "Filters & match rules (optional)",
  "多个值用逗号分隔；留空表示不过滤。":
    "Separate multiple values with commas; leave empty to match all.",
  "事件类型 (events)": "Events",
  "动作 (actions)": "Actions",
  "结论 / 状态 (conclusions)": "Conclusions",
  "分支 (branches)": "Branches",
  "仓库 (repositories)": "Repositories",
  "Match Key 提取路径": "Match Key JSON path",
  "JSON 字段精确匹配": "JSON field matches",
  "每行一条 path=value，例如 status=failed":
    "One path=value per line, e.g. status=failed",
  保存更改: "Save changes",
  创建触发器: "Create trigger",
  触发器已启用: "Trigger enabled",
  触发器已停用: "Trigger disabled",
  更新触发器失败: "Failed to update trigger",
  触发器已删除: "Trigger deleted",
  删除触发器失败: "Failed to delete trigger",
  "Webhook 密钥已轮换": "Webhook secret rotated",
  轮换密钥失败: "Failed to rotate secret",
  触发器已创建: "Trigger created",
  触发器已更新: "Trigger updated",
  保存触发器失败: "Failed to save trigger",
  等待与定时唤醒: "Watchers & wakeups",
  收起历史: "Hide history",
  "历史 ({{count}})": "History ({{count}})",
  "当前没有挂起中的事件等待或定时自唤醒。":
    "No armed event watchers or delayed wakeups on this card.",
  已取消该等待项: "Watcher cancelled",
  取消等待失败: "Failed to cancel watcher",
  "复制 Match Key": "Copy Match Key",
  "有 {{count}} 个挂起等待项": "{{count}} pending watcher(s)",
  "Webhook 接入": "Webhooks",
  "接入外部事件自动唤醒任务，也可直接让 AI 自动帮你配置。":
    "Connect external webhooks to wake tasks automatically, or let the AI set them up for you.",
  已配置的接入: "Configured webhooks",
  添加接入: "Add webhook",
  "还没有配置任何 Webhook 接入": "No webhooks configured yet",
  自动唤醒等待任务: "Wake waiting tasks",
  发送到指定任务: "Send to specific task",
  自动新建待办任务: "Create new todo task",
  最近触发于: "Last triggered",
  尚未触发: "Never triggered",
  通用: "Generic",
  启用接入: "Enable webhook",
  停用接入: "Disable webhook",
  已启用该接入: "Webhook enabled",
  已停用该接入: "Webhook disabled",
  更新失败: "Update failed",
  等待中的任务: "Waiting tasks",
  "AI 挂起等待外部结果或定时提醒的任务会显示在这里。":
    "Tasks paused for external events or scheduled reminders appear here.",
  当前等待中: "Currently waiting",
  全部等待记录: "All records",
  只看等待中: "Waiting only",
  查看历史: "View history",
  暂无历史等待记录: "No history yet",
  当前没有等待中的任务: "No waiting tasks right now",
  等待外部事件: "Waiting for event",
  定时唤醒: "Scheduled wakeup",
  已取消等待: "Wait cancelled",
  "添加 Webhook 接入": "Add webhook",
  "已添加 Webhook 接入": "Webhook added",
  接入详情: "Webhook details",
  已保存修改: "Changes saved",
  已生成新密钥: "New secret generated",
  已删除该接入: "Webhook deleted",
  保存回调密钥: "Save webhook secret",
  "密钥只显示这一次，关闭后无法找回。请立即复制并配置到外部平台。":
    "This secret is shown only once and cannot be retrieved later. Copy it to your external platform now.",
  回调地址: "Webhook URL",
  已复制回调地址: "Webhook URL copied",
  签名密钥: "Signing secret",
  已复制密钥: "Secret copied",
  我已保存: "I saved it",
  密钥尾号: "Secret hint",
  复制回调地址: "Copy URL",
  编辑配置: "Edit",
  查看最近记录: "Recent logs",
  "生成新密钥后旧密钥会立即失效，外部平台需同步更新。确认继续？":
    "The old secret stops working immediately. Update your external platform with the new secret. Continue?",
  删除接入: "Delete webhook",
  "删除后此回调地址将不再接收外部事件。确认删除？":
    "This endpoint will stop receiving external events once deleted. Continue?",
  删除失败: "Delete failed",
  还没有收到过外部事件: "No external events received yet",
  未满足筛选: "Filtered",
  已忽略: "Ignored",
  验证失败: "Auth failed",
  "例如：GitHub 构建通知": "e.g. GitHub CI notifications",
  来源: "Provider",
  启用该接入: "Enable this webhook",
  高级选项: "Advanced options",
  收到事件后: "When triggered",
  自动唤醒等待中的任务: "Automatically wake waiting tasks",
  执行方式: "Dispatch",
  优先插队: "Interrupt current run",
  目标任务: "Target task",
  请选择任务: "Select a task",
  验证方式: "Authentication",
  签名验证: "Signature (HMAC)",
  令牌验证: "Bearer token",
  仅接收这些事件: "Events",
  "逗号分隔，留空表示全部": "Comma-separated, empty for all",
  仅接收这些动作: "Actions",
  "例如：completed": "e.g. completed",
  仅接收这些结果: "Conclusions",
  "例如：success, failure": "e.g. success, failure",
  限定分支: "Branches",
  "例如：main, release/*": "e.g. main, release/*",
  限定仓库: "Repositories",
  "例如：owner/repo": "e.g. owner/repo",
  关联标识字段路径: "Match key path",
  额外字段筛选: "Extra field filters",
  "每行一条，格式：字段路径=期望值": "One per line: field.path=expected",
  保存修改: "Save changes",
  创建接入: "Create webhook",
  等待与定时提醒: "Waiting & reminders",
  "当前没有等待中的事件或定时提醒。": "No waiting events or reminders.",
  预计唤醒: "Scheduled for",
  标为未读: "Mark as unread",
  标为已读: "Mark as read",
  已标为未读: "Marked as unread",
  已标为已读: "Marked as read",
  未读: "Unread",
  以下为未读消息: "Unread messages",
  "1 条未读消息": "1 unread message",
  "{{count}} 条未读消息": "{{count}} unread messages",
  未读状态更新失败: "Failed to update unread state",
  置顶: "Pin",
  取消置顶: "Unpin",
  置顶讨论: "Pin discussion",
  置顶任务: "Pin task",
  置顶定时任务: "Pin scheduled task",
  编辑信息: "Edit details",
  删除讨论: "Delete discussion",
  删除任务: "Delete task",
  删除定时任务: "Delete scheduled task",
  下次: "Next",
  今天: "Today",
  明天: "Tomorrow",
  上次执行失败: "Last run failed",
  需关注: "Needs attention",
  免打扰: "Muted",
  免打扰未读: "Muted unread",
  静默未读: "Muted unread",
  全部已读: "Mark all read",
  "已全部标为已读 ({{count}})": "Marked all as read ({{count}})",
  "已标为已读 {{succeeded}} 条，{{failed}} 条失败":
    "Marked {{succeeded}} as read, {{failed}} failed",
  批量标为已读失败: "Failed to mark all as read",
  "已暂停 / 已归档": "Paused / Archived",
  总览: "Overview",
  查看定时任务总览: "View scheduled tasks overview",
  "搜索定时任务…": "Search scheduled tasks...",
  没有匹配的定时任务: "No matching scheduled tasks",
  完成: "Done",
  验收完成: "Accept & complete",
  已验收完成: "Accepted and completed",
  验收失败: "Failed to accept",
  批量验收: "Batch accept",
  全选: "Select all",
  取消全选: "Deselect all",
  "完成所选 ({{count}})": "Complete selected ({{count}})",
  "已完成 {{count}} 张卡片": "Completed {{count}} cards",
  "已完成 {{succeeded}} 张卡片，{{failed}} 张失败":
    "Completed {{succeeded}} cards, {{failed}} failed",
  "批量验收失败（{{failed}} 张）": "Batch accept failed ({{failed}})",
  "正在加载授权页…": "Loading authorization page…",
  授权访问工作空间: "Authorize MCP access",
  "无法完成本次授权请求。": "Unable to complete this authorization request.",
  "OAuth 授权服务暂时不可用，请稍后回到客户端重试。":
    "OAuth authorization is temporarily unavailable. Return to the client and try again.",
  "授权请求无效或已失效，请回到客户端重新发起授权。":
    "This authorization request is invalid or expired. Return to the client and start again.",
  "授权参数不完整，请回到客户端重新发起授权。":
    "Missing authorization parameters. Return to the client and start again.",
  返回工作台: "Back to workspace",
  "未命名 MCP 客户端": "Unnamed MCP client",
  "请确认是否允许该外部客户端通过 MCP 接入你的 CycleDock 工作空间。":
    "Confirm whether to allow this external client to connect to your CycleDock workspace via MCP.",
  请求客户端: "Client",
  目标工作空间: "Workspace",
  授权回调地址: "Redirect URI",
  "独立的 OAuth Client Source": "Dedicated OAuth Client Source",
  "为该客户端分配独立身份标识，可在设置中随时查看或撤销授权。":
    "Assigns an isolated client identity that you can audit or revoke anytime in settings.",
  "通过 MCP 读取和更新工作": "Read and update work via MCP",
  "可读取项目与卡片上下文，并提交进度回复、巡检结论与待验收确认。":
    "Can read project and card context, post replies, record check-ins, and confirm work for review.",
  人工验收边界受保护: "Human review boundary enforced",
  "不可代替人类完成 Review → Done 最终验收。":
    "Cannot complete final Review → Done approval on behalf of a human.",
  授权: "Authorize",
  "正在授权…": "Authorizing…",
  "正在处理…": "Processing…",
  "授权后可随时在 CycleDock 设置 → AI 客户端中撤销访问权限。":
    "You can revoke access anytime in CycleDock Settings → AI Clients.",
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
