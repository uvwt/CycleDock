import { useState, type ReactNode } from "react";
import { Plus, MoreHorizontal, Info, Bell } from "lucide-react";
import {
  Button,
  IconButton,
  Input,
  Textarea,
  LoadingButton,
  Typography,
  Avatar,
  Badge,
  Switch,
  Separator,
} from "../../components/ui/controls";
import {
  Select,
  SelectTrigger,
  SelectContent,
  SelectItem,
  SelectValue,
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogTitle,
  DialogDescription,
  Sheet,
  SheetTrigger,
  SheetContent,
  SheetTitle,
  SheetDescription,
  Popover,
  PopoverTrigger,
  PopoverContent,
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  Tooltip,
  TooltipTrigger,
  TooltipContent,
  TooltipProvider,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "../../components/ui/overlays";
import {
  Skeleton,
  SidebarSkeleton,
  ProjectSwitcherSkeleton,
  DiscussionListSkeleton,
  ThreadSkeleton,
  BoardSkeleton,
  CardDetailSkeleton,
  SettingsSkeleton,
  EmptyState,
  ErrorState,
  DelayedSkeleton,
} from "../../components/ui/feedback";
import {
  AgentMessage,
  HumanMessage,
  SystemEvent,
  AgentRunMessage,
} from "../../components/ui/messages";
import {
  Sidebar,
  PageHeader,
  MobileNavButton,
} from "../../components/layout/Scaffold";
import "../../styles/tokens.css";

function DemoSection({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="v2-panel v2-showcase-section">
      <Typography as="h2" scale="section">
        {title}
      </Typography>
      {children}
    </section>
  );
}

export default function UiShowcase() {
  const [mode, setMode] = useState<"discussions" | "work">("discussions");
  const [project, setProject] = useState("CycleDock");
  const [name, setName] = useState("");
  const [loading, setLoading] = useState(false);
  const [skeletons, setSkeletons] = useState(false);
  const [theme, setTheme] = useState<"light" | "dark">("light");
  return (
    <TooltipProvider delayDuration={250}>
      <div
        className="v2 v2-showcase-layout"
        data-theme={theme}
        data-testid="v2-showcase"
      >
        <Sidebar
          className="min-h-[100dvh]"
          project={project}
          account="Administrator"
          mode={mode}
          onModeChange={setMode}
          onProjectClick={() =>
            setProject(project === "CycleDock" ? "NexusDock" : "CycleDock")
          }
        >
          <div className="v2-showcase-stack mb-3">
            <Input aria-label="搜索讨论" placeholder="搜索讨论" />
            <IconButton icon={Plus} label="新建讨论" variant="ghost" />
          </div>
          <nav aria-label="讨论列表" className="space-y-1">
            {[
              "Frontend V2 设计系统",
              "讨论列表加载与滚动",
              "工作详情和执行状态",
            ].map((item, i) => (
              <Button
                key={item}
                variant={i === 0 ? "secondary" : "ghost"}
                className="w-full justify-start text-left"
                size="compact"
              >
                {item}
              </Button>
            ))}
          </nav>
        </Sidebar>
        <main className="v2-showcase-content">
          <PageHeader
            title="Frontend V2 · UI Showcase"
            description="独立设计系统验收页 · 不使用旧业务页面 JSX"
            leading={
              <div className="md:hidden">
                <Sheet>
                  <SheetTrigger asChild>
                    <MobileNavButton onClick={() => {}} />
                  </SheetTrigger>
                  <SheetContent>
                    <SheetTitle>导航</SheetTitle>
                    <SheetDescription>移动设备上的工作入口</SheetDescription>
                    <div className="mt-5 space-y-2">
                      <Button onClick={() => setMode("discussions")}>
                        讨论
                      </Button>
                      <Button onClick={() => setMode("work")}>工作</Button>
                    </div>
                  </SheetContent>
                </Sheet>
              </div>
            }
            actions={
              <>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <IconButton icon={Bell} label="通知" variant="ghost" />
                  </TooltipTrigger>
                  <TooltipContent>通知</TooltipContent>
                </Tooltip>
                <Button
                  size="compact"
                  variant="ghost"
                  onClick={() => setTheme(theme === "light" ? "dark" : "light")}
                >
                  主题：{theme === "light" ? "浅色" : "深色"}
                </Button>
              </>
            }
          />
          <div className="v2-showcase-grid">
            <DemoSection title="字号与层级">
              <div className="space-y-2">
                <Typography scale="title">页面标题 22px</Typography>
                <Typography scale="section">区块标题 16px</Typography>
                <Typography scale="body">
                  正文内容 14px：查看任务的进展与结果。
                </Typography>
                <Typography scale="control">导航和表单 13px</Typography>
                <Typography scale="meta">辅助文字、时间 12px</Typography>
              </div>
            </DemoSection>
            <DemoSection title="按钮与状态">
              <div className="v2-showcase-stack">
                <Button variant="primary">保存修改</Button>
                <Button>次要操作</Button>
                <Button variant="ghost">文本操作</Button>
                <Button variant="danger">删除</Button>
              </div>
              <div className="v2-showcase-stack">
                <Button size="compact">紧凑 32</Button>
                <Button>默认 36</Button>
                <Button size="prominent">强调 40</Button>
                <IconButton icon={MoreHorizontal} label="更多操作" />
              </div>
              <div className="v2-showcase-stack">
                <LoadingButton
                  loading={loading}
                  onClick={() => setLoading(!loading)}
                >
                  {loading ? "保存中…" : "演示加载"}
                </LoadingButton>
                <Button onClick={() => setLoading(false)}>重置</Button>
                <Button disabled>禁用</Button>
              </div>
            </DemoSection>
            <DemoSection title="输入与选择">
              <label className="block space-y-1">
                <span className="v2-text-control">项目名称</span>
                <Input
                  placeholder="输入项目名称"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label className="block space-y-1">
                <span className="v2-text-control">任务说明</span>
                <Textarea placeholder="描述要执行的任务" rows={2} />
              </label>
              <label className="block space-y-1">
                <span className="v2-text-control">任务类型</span>
                <Select defaultValue="discussion">
                  <SelectTrigger aria-label="任务类型">
                    <SelectValue placeholder="选择类型" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="discussion">
                      讨论 · Discussion
                    </SelectItem>
                    <SelectItem value="todo">待办 · Todo</SelectItem>
                    <SelectItem value="ongoing">持续事项 · Ongoing</SelectItem>
                  </SelectContent>
                </Select>
              </label>
              <label className="v2-showcase-stack">
                <Switch aria-label="开启通知" defaultChecked />
                开启通知
              </label>
            </DemoSection>
            <DemoSection title="弹层与菜单">
              <div className="v2-showcase-stack">
                <Dialog>
                  <DialogTrigger asChild>
                    <Button>新建项目 Dialog</Button>
                  </DialogTrigger>
                  <DialogContent>
                    <DialogTitle className="v2-text-section">
                      新建项目
                    </DialogTitle>
                    <DialogDescription className="v2-text-control mt-2 text-[var(--v2-muted)]">
                      项目创建后保持当前讨论或工作模式。
                    </DialogDescription>
                    <label className="mt-4 block space-y-1">
                      <span>名称</span>
                      <Input placeholder="请输入项目名称" />
                    </label>
                    <div className="mt-5">
                      <Button variant="primary">创建项目</Button>
                    </div>
                  </DialogContent>
                </Dialog>
                <Sheet>
                  <SheetTrigger asChild>
                    <Button>详情 Sheet</Button>
                  </SheetTrigger>
                  <SheetContent>
                    <SheetTitle className="v2-text-section">
                      任务信息
                    </SheetTitle>
                    <SheetDescription className="v2-text-control mt-2 text-[var(--v2-muted)]">
                      窄屏不占用主内容宽度。
                    </SheetDescription>
                    <p className="mt-4">来源：Frontend V2 设计讨论</p>
                  </SheetContent>
                </Sheet>
                <Popover>
                  <PopoverTrigger asChild>
                    <Button>详情 Popover</Button>
                  </PopoverTrigger>
                  <PopoverContent className="w-64">
                    <Typography scale="section">任务信息</Typography>
                    <Typography scale="control" className="mt-2">
                      不使用固定右侧栏。
                    </Typography>
                  </PopoverContent>
                </Popover>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <IconButton icon={MoreHorizontal} label="更多菜单" />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent>
                    <DropdownMenuItem onSelect={() => setProject("CycleDock")}>
                      切换 CycleDock
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => setProject("NexusDock")}>
                      切换 NexusDock
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
              <div className="v2-showcase-stack">
                <Avatar fallback="C" />
                <Badge>未开始</Badge>
                <Badge tone="success">已完成</Badge>
                <Badge tone="danger">失败</Badge>
                <Badge tone="accent">运行中</Badge>
              </div>
            </DemoSection>
            <DemoSection title="页签与空态">
              <Tabs defaultValue="all">
                <TabsList>
                  <TabsTrigger value="all">全部</TabsTrigger>
                  <TabsTrigger value="review">待审核</TabsTrigger>
                  <TabsTrigger value="ongoing">持续事项</TabsTrigger>
                </TabsList>
                <TabsContent value="all">
                  <EmptyState
                    title="暂无任务"
                    description="可以从讨论生成工作，也可以直接新建待办。"
                    action={
                      <Button>
                        <Plus size={14} />
                        新建待办
                      </Button>
                    }
                  />
                </TabsContent>
                <TabsContent value="review">
                  <EmptyState title="没有待审核任务" />
                </TabsContent>
                <TabsContent value="ongoing">
                  <EmptyState title="没有持续事项" />
                </TabsContent>
              </Tabs>
            </DemoSection>
            <DemoSection title="错误与恢复">
              <ErrorState
                title="无法获取项目"
                message="连接失败，请检查网络后重试。"
                onRetry={() => {}}
              />
              <Separator />
              <div className="v2-showcase-stack">
                <Info size={16} color="var(--v2-muted)" />
                <Typography scale="meta">
                  空态、错误、加载态分离表达。
                </Typography>
              </div>
            </DemoSection>
          </div>
          <DemoSection title="Card Timeline · Human / Agent / System">
            <div className="mx-auto max-w-3xl space-y-5">
              <HumanMessage>
                调整讨论和工作模式的字体大小，所有 Select 与按钮统一高度。
              </HumanMessage>
              <AgentRunMessage status="running" duration="18 秒">
                正在建立统一组件规格，并校验桌面和移动端布局。
              </AgentRunMessage>
              <AgentMessage duration="1 分 08 秒">
                Design Tokens 已集中定义。消息在不同类型的 Card
                中共享展示结构，Run 耗时属于该次执行。
              </AgentMessage>
              <SystemEvent>任务已进入待审核 · 10:42</SystemEvent>
              <HumanMessage>收到，继续检查 Skeleton。</HumanMessage>
            </div>
          </DemoSection>
          <DemoSection title="Skeleton 场景">
            <div className="v2-showcase-stack">
              <Button onClick={() => setSkeletons(!skeletons)}>
                {skeletons ? "隐藏骨架" : "显示骨架"}
              </Button>
              <Typography scale="meta">
                只在首次加载、延迟 180ms 后显示
              </Typography>
            </div>
            {skeletons ? (
              <DelayedSkeleton>
                <div className="v2-showcase-grid">
                  <div className="v2-panel p-2">
                    <Typography scale="control">Sidebar / Project</Typography>
                    <div className="mt-2 h-96">
                      <SidebarSkeleton />
                    </div>
                    <ProjectSwitcherSkeleton />
                    <DiscussionListSkeleton />
                  </div>
                  <div className="v2-panel p-2">
                    <Typography scale="control">Thread</Typography>
                    <ThreadSkeleton />
                  </div>
                  <div className="v2-panel p-2">
                    <Typography scale="control">Card Detail</Typography>
                    <CardDetailSkeleton />
                  </div>
                  <div className="v2-panel p-2">
                    <Typography scale="control">Settings</Typography>
                    <SettingsSkeleton />
                  </div>
                </div>
                <div className="mt-4">
                  <BoardSkeleton />
                </div>
              </DelayedSkeleton>
            ) : (
              <div className="flex items-center gap-2">
                <Skeleton className="h-4 w-24" />
                <Skeleton className="h-4 w-44" />
              </div>
            )}
          </DemoSection>
          <footer className="v2-text-meta pb-6">
            Frontend V2 · Development-only Showcase · CycleDock
          </footer>
        </main>
      </div>
    </TooltipProvider>
  );
}
