// ifami 后端单二进制入口：serve api / serve worker / migrate
// 架构说明见 docs/技术方案设计.md §3（模块化单体，模块边界=未来服务边界）
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	_ "github.com/go-sql-driver/mysql"
	gomigrate "github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/file"
	"gorm.io/gorm"

	"ifami/internal/identity"
	"ifami/internal/ingest"
	"ifami/internal/notify"
	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/config"
	"ifami/internal/pkg/crypto"
	"ifami/internal/pkg/db"
	"ifami/internal/pkg/logx"
	"ifami/internal/pkg/redisx"
	"ifami/internal/pkg/wechat"
	"ifami/internal/schedule"
	"ifami/internal/task"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	service := os.Args[1]
	if service == "serve" && len(os.Args) > 2 {
		service = os.Args[2]
	}
	logx.Configure(os.Stdout, service, os.Getenv("LOG_LEVEL"))
	hlog.SetLogger(logx.FrameworkLogger{FullLogger: hlog.DefaultLogger()})
	switch os.Args[1] {
	case "serve":
		if len(os.Args) < 3 {
			usage()
			os.Exit(1)
		}
		switch os.Args[2] {
		case "api":
			serveAPI()
		case "worker":
			serveWorker()
		default:
			usage()
			os.Exit(1)
		}
	case "migrate":
		migrate()
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("用法: ifami serve api|worker | migrate")
}

// initCore 初始化 API 与 Worker 共用的核心依赖（DB/Redis/微信/加密/会话/identity）。
func initCore(cfg *config.Config) (*gorm.DB, *identity.Service, auth.SessionStore, *crypto.FieldCipher) {
	gdb, err := db.Open(cfg.RDSDSN)
	if err != nil {
		logx.Fatalf("连接数据库失败: %v", err)
	}
	rdb := redisx.New(cfg.RedisAddr)
	probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	info, probeErr := rdb.Info(probeCtx, "server").Result()
	cancel()
	version := "unknown"
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			candidate := strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
			if len(candidate) < 32 {
				version = candidate
			}
		}
	}
	if probeErr != nil {
		logx.Warn(context.Background(), "redis_version_unavailable", logx.ErrorFields(probeErr)...)
	} else {
		logx.Info(context.Background(), "redis_version", "redis_version", version)
	}
	wx := wechat.NewMiniClient(cfg.WXMiniAppID, cfg.WXMiniSecret)
	logx.Info(context.Background(), "configuration", "appid_set", cfg.WXMiniAppID != "", "appsecret_set", cfg.WXMiniSecret != "")

	// 字段级加密是合规铁律（手机号/孩子姓名），密钥必须配置。
	field, err := crypto.NewFieldCipher(cfg.FieldEncKey)
	if err != nil {
		logx.Fatalf("字段加密密钥无效（FIELD_ENC_KEY 需为 base64 编码的 32 字节）: %v", err)
	}
	sessions := auth.NewHybridStore(gdb, rdb)

	idSvc := identity.NewService(
		identity.NewStore(gdb), wx, cfg.WXMiniAppID,
		sessions, field, cfg.InviteSecret,
	)
	idSvc.SetJoinCodeRegistry(identity.NewRedisJoinCodeRegistry(rdb, field, cfg.InviteSecret, "ifami:", identity.JoinProtectionOptions{
		UserLimit: cfg.JoinFailedUserLimit, IPLimit: cfg.JoinFailedIPLimit, Window: cfg.JoinFailedWindow,
	}))
	return gdb, idSvc, sessions, field
}

// scheduleCreator 把 schedule.Service 适配成 ingest 消费方定义的 ScheduleCreator 接口。
type scheduleCreator struct{ svc *schedule.Service }

func (a scheduleCreator) CreateBatch(ctx context.Context, userID, familyID, jobID uint64, items []ingest.ScheduleItem, force bool) ([]uint64, error) {
	inputs := make([]schedule.ScheduleInput, 0, len(items))
	for _, it := range items {
		inputs = append(inputs, schedule.ScheduleInput{
			Title: it.Title, Type: it.Type, ChildMemberID: it.ChildMemberID, StartDate: it.StartDate,
			RRule: it.RRule, StartTime: it.StartTime, EndTime: it.EndTime,
			Location: it.Location, Note: it.Note, OwnerMemberID: it.OwnerMemberID,
		})
	}
	res, err := a.svc.CreateSchedulesBatch(ctx, userID, familyID, inputs, force, fmt.Sprintf("ingest:%d", jobID))
	if err != nil {
		return nil, err
	}
	return res.ScheduleIDs, nil
}

func (a scheduleCreator) FindBatchResult(ctx context.Context, familyID, jobID uint64) ([]uint64, bool, error) {
	return a.svc.FindBatchResult(ctx, familyID, fmt.Sprintf("ingest:%d", jobID))
}

type ingestNotifier struct{ svc *notify.Service }

func (a ingestNotifier) CreateJob(ctx context.Context, in ingest.NotifyInput) (uint64, error) {
	return a.svc.CreateJob(ctx, &notify.CreateJobInput{
		FamilyID: in.FamilyID, TargetUserID: in.TargetUserID, Level: in.Level,
		Payload: in.Payload, BusinessKey: in.BusinessKey,
	})
}

// newIngest 组装 ingest 模块。INGEST_DIR 不可用时直接退出（拍照导入必需）；方舟未配置只告警，start 时识别失败。
func newIngest(cfg *config.Config, gdb *gorm.DB, idSvc *identity.Service, schedSvc *schedule.Service, notifSvc *notify.Service, field *crypto.FieldCipher) *ingest.Service {
	imgs, err := ingest.NewLocalImageStore(cfg.IngestDir)
	if err != nil {
		logx.Fatalf("拍照导入临时目录不可用: %v", err)
	}
	ark := ingest.NewArkClient(cfg.ArkBaseURL, cfg.ArkAPIKey, cfg.ArkModel)
	if !ark.Configured() {
		logx.Warnf("ARK_API_KEY / ARK_MODEL 未配置，拍照导入识别将失败")
	}
	return ingest.NewService(ingest.NewStore(gdb), imgs, ark, idSvc, scheduleCreator{schedSvc}, ingestNotifier{notifSvc}, field, wechat.NewMiniClient(cfg.WXMiniAppID, cfg.WXMiniSecret))
}

// notifyAdapter 把 notify.Service 适配成 schedule/task 消费方定义的 Notifier 接口。
// 拆两个类型是因 Go 无方法重载：schedule.NotifyInput ≠ task.NotifyInput，无法同名方法。
type scheduleNotifier struct{ svc *notify.Service }

func (a scheduleNotifier) CreateJob(ctx context.Context, in schedule.NotifyInput) (uint64, error) {
	return a.svc.CreateJob(ctx, &notify.CreateJobInput{
		FamilyID: in.FamilyID, TargetUserID: in.TargetUserID, Level: in.Level,
		Payload: in.Payload, BusinessKey: in.BusinessKey,
	})
}

type taskNotifier struct{ svc *notify.Service }

func (a taskNotifier) CreateJob(ctx context.Context, in task.NotifyInput) (uint64, error) {
	return a.svc.CreateJob(ctx, &notify.CreateJobInput{
		FamilyID: in.FamilyID, TargetUserID: in.TargetUserID, Level: in.Level,
		Payload: in.Payload, BusinessKey: in.BusinessKey,
	})
}

func newNotifyChannel(cfg *config.Config) notify.Channel {
	if strings.EqualFold(cfg.NotifyChannel, "mock") {
		logx.Warnf("NOTIFY_CHANNEL=mock，触达消息只记录日志，不会真实发送")
		return notify.MockSubscribe{}
	}
	return notify.NewWechatSubscribe(wechat.NewMiniClient(cfg.WXMiniAppID, cfg.WXMiniSecret), notify.SubscribeTemplates{
		Schedule: cfg.WXSubscribeScheduleTemplate,
		Task:     cfg.WXSubscribeTaskTemplate,
		Feedback: cfg.WXSubscribeFeedbackTemplate,
	})
}

// serveAPI 启动 HTTP 服务（Hertz）。依赖注入见此处。
func serveAPI() {
	cfg := config.Load()
	gdb, idSvc, sessions, field := initCore(cfg)

	notifSvc := notify.NewService(notify.NewStore(gdb), idSvc, newNotifyChannel(cfg))
	schedSvc := schedule.NewService(schedule.NewStore(gdb), idSvc, scheduleNotifier{notifSvc})
	notifSvc.SetScheduleVisibility(schedSvc)
	taskSvc := task.NewService(task.NewStore(gdb), idSvc, taskNotifier{notifSvc}, wechat.NewMiniClient(cfg.WXMiniAppID, cfg.WXMiniSecret))
	ingestSvc := newIngest(cfg, gdb, idSvc, schedSvc, notifSvc, field)

	// 拍照导入单张图 ≤5MB，multipart 开销留余量，请求体上限调到 10MB（默认 4MB）。
	h := server.Default(server.WithHostPorts(cfg.APIAddr), server.WithMaxRequestBodySize(10<<20))
	h.Use(accessLog())
	h.GET("/healthz", healthz)
	v1 := h.Group("/v1")
	identity.NewHandler(idSvc).Register(v1)
	schedule.NewHandler(schedSvc, sessions).Register(v1)
	task.NewHandler(taskSvc, sessions).Register(v1)
	notify.NewHandler(notifSvc, sessions).Register(v1)
	ingest.NewHandler(ingestSvc, sessions).Register(v1)

	logx.Infof("api 监听 %s", cfg.APIAddr)
	h.Spin()
}

func healthz(ctx context.Context, c *app.RequestContext) {
	c.JSON(200, map[string]string{"status": "ok"})
}

func accessLog() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		started := time.Now()
		id := string(c.GetHeader("X-Request-Id"))
		if !logx.ValidID(id) {
			id = logx.ID()
		}
		ctx = logx.With(ctx, "request_id", id)
		c.Header("X-Request-Id", id)
		c.Set("logx.context", ctx)

		method := string(c.Request.Method())
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		if string(c.URI().Path()) == "/healthz" {
			c.Next(ctx)
			return
		}
		attrs := []any{"method", method, "route", route}
		if fid, err := strconv.ParseUint(string(c.GetHeader("X-Family-Id")), 10, 64); err == nil && fid > 0 {
			attrs = append(attrs, "requested_family_id", fid)
		}
		logx.Info(ctx, "request_in", attrs...)
		defer func() {
			if value := recover(); value != nil {
				panicAttrs := append(append([]any(nil), attrs...), "status", 500, "outcome", "failed", "duration_ms", float64(time.Since(started).Microseconds())/1000, "code_locations", logx.CodeLocations())
				if uid, ok := c.Get("logx.user_id"); ok {
					panicAttrs = append(panicAttrs, "user_id", uid)
				}
				logx.Error(ctx, "request_panicked", panicAttrs...)
				panic(value)
			}
		}()

		c.Next(ctx)
		status := c.Response.StatusCode()
		elapsed := time.Since(started)
		outcome := "succeeded"
		level := slog.LevelInfo
		if status >= 400 {
			outcome = "rejected"
		}
		if status >= 500 {
			outcome = "failed"
			level = slog.LevelError
		} else if elapsed > time.Second {
			level = slog.LevelWarn
		}
		attrs = append(attrs, "status", status, "duration_ms", float64(elapsed.Microseconds())/1000, "outcome", outcome)
		if code, ok := c.Get("logx.code"); ok {
			attrs = append(attrs, "code", code)
		}
		if uid, ok := c.Get("logx.user_id"); ok {
			attrs = append(attrs, "user_id", uid)
		}
		logx.Write(ctx, level, "request_out", attrs...)
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// serveWorker 异步 Worker（30s 粒度）：触达轮询与升级（ADR-002）、打卡提醒（R3.3）、
// 拍照导入任务接管与残留原图清理（§5.5）；重复日程实例每日续展（ADR-003）。
func serveWorker() {
	cfg := config.Load()
	gdb, idSvc, _, field := initCore(cfg)
	notifSvc := notify.NewService(notify.NewStore(gdb), idSvc, newNotifyChannel(cfg))
	schedSvc := schedule.NewService(schedule.NewStore(gdb), idSvc, scheduleNotifier{notifSvc})
	notifSvc.SetScheduleVisibility(schedSvc)
	taskSvc := task.NewService(task.NewStore(gdb), idSvc, taskNotifier{notifSvc}, wechat.NewMiniClient(cfg.WXMiniAppID, cfg.WXMiniSecret))
	ingestSvc := newIngest(cfg, gdb, idSvc, schedSvc, notifSvc, field)

	logx.Infof("worker 启动，扫描间隔 30s")
	ctx := context.Background()
	lastExtend := ""
	scan := func() {
		ctx := logx.With(ctx, "batch_id", logx.ID())
		started := time.Now()
		now := time.Now().UTC()
		_ = scanStep(ctx, "notify", func() error { return notifSvc.ProcessDue(ctx) })
		_ = scanStep(ctx, "task.remind", func() error { return taskSvc.RemindDue(ctx, now) })
		_ = scanStep(ctx, "schedule.remind", func() error { return schedSvc.RemindDue(ctx, now) })
		_ = scanStep(ctx, "ingest.recover", func() error { return ingestSvc.RecoverStale(ctx) })
		ingestSvc.CleanupOrphans()
		// 续展每天一次（进程启动后首轮即执行一次）。
		if day := now.Format("2006-01-02"); day != lastExtend {
			n, err := schedSvc.ExtendInstances(ctx, now)
			if err != nil {
				logx.Failure(ctx, "schedule.extend", err)
			} else {
				lastExtend = day
				logx.Info(ctx, "instances_extended", "count", n)
			}
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			logx.Warn(ctx, "worker_batch_slow", "duration_ms", float64(elapsed.Microseconds())/1000.0)
		}
	}
	scan()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for range tick.C {
		scan()
	}
}

func migrate() {
	cfg := config.Load()
	if cfg.RDSDSN == "" {
		logx.Fatalf("RDS_DSN 未配置，无法迁移")
	}
	sqlDB, err := sql.Open("mysql", withMultiStatements(cfg.RDSDSN))
	if err != nil {
		logx.Fatalf("打开数据库失败: %v", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.Ping(); err != nil {
		logx.Fatalf("连接数据库失败: %v", err)
	}

	dbDriver, err := migratemysql.WithInstance(sqlDB, &migratemysql.Config{})
	if err != nil {
		logx.Fatalf("初始化数据库驱动失败: %v", err)
	}

	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		dir = "migrations"
	}
	srcDriver, err := (&file.File{}).Open("file://" + dir)
	if err != nil {
		logx.Fatalf("读取迁移目录失败: %v", err)
	}

	m, err := gomigrate.NewWithInstance("file", srcDriver, "mysql", dbDriver)
	if err != nil {
		logx.Fatalf("初始化迁移器失败: %v", err)
	}
	if err := m.Up(); err != nil {
		if errors.Is(err, gomigrate.ErrNoChange) {
			fmt.Println("数据库已是最新，无需迁移")
			return
		}
		logx.Fatalf("迁移失败: %v", err)
	}
	fmt.Println("迁移完成")
}

// withMultiStatements 给 DSN 追加 multiStatements=true：
// golang-migrate 的 MySQL 驱动依赖它执行单文件内的多条建表语句。
func withMultiStatements(dsn string) string {
	if strings.Contains(dsn, "multiStatements") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "multiStatements=true"
}

func scanStep(ctx context.Context, name string, fn func() error) error {
	done := logx.Operation(ctx, "worker."+name, 5*time.Second)
	err := fn()
	done(err)
	return err
}
