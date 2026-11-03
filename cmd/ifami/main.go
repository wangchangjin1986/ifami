// ifami 后端单二进制入口：serve api / serve worker / migrate
// 架构说明见 docs/技术方案设计.md §3（模块化单体，模块边界=未来服务边界）
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
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
	wx := wechat.NewMiniClient(cfg.WXMiniAppID, cfg.WXMiniSecret)
	// 临时调试：打印微信凭据以定位 code2session 40125（invalid appsecret）。
	// %q 会把尾随空格/换行等隐藏字符显式转义出来；定位后请删除本行并轮转日志，避免凭据长期留存。
	logx.Infof("微信小程序凭据: appid=%s appsecret=%q len=%d", cfg.WXMiniAppID, cfg.WXMiniSecret, len(cfg.WXMiniSecret))

	// 字段级加密是合规铁律（手机号/孩子姓名），密钥必须配置。
	field, err := crypto.NewFieldCipher(cfg.FieldEncKey)
	if err != nil {
		logx.Fatalf("字段加密密钥无效（FIELD_ENC_KEY 需为 base64 编码的 32 字节）: %v", err)
	}
	sessions := auth.NewRedisStore(rdb)

	idSvc := identity.NewService(
		identity.NewStore(gdb), wx, cfg.WXMiniAppID,
		sessions, field, cfg.InviteSecret,
	)
	return gdb, idSvc, sessions, field
}

// scheduleCreator 把 schedule.Service 适配成 ingest 消费方定义的 ScheduleCreator 接口。
type scheduleCreator struct{ svc *schedule.Service }

func (a scheduleCreator) CreateBatch(ctx context.Context, userID, familyID uint64, items []ingest.ScheduleItem, force bool) ([]uint64, error) {
	inputs := make([]schedule.ScheduleInput, 0, len(items))
	for _, it := range items {
		inputs = append(inputs, schedule.ScheduleInput{
			Title: it.Title, Type: it.Type, ChildMemberID: it.ChildMemberID, StartDate: it.StartDate,
			RRule: it.RRule, StartTime: it.StartTime, EndTime: it.EndTime,
			Location: it.Location, Note: it.Note, OwnerMemberID: it.OwnerMemberID,
		})
	}
	res, err := a.svc.CreateSchedulesBatch(ctx, userID, familyID, inputs, force)
	if err != nil {
		return nil, err
	}
	return res.ScheduleIDs, nil
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
	return ingest.NewService(ingest.NewStore(gdb), imgs, ark, idSvc, scheduleCreator{schedSvc}, ingestNotifier{notifSvc}, field)
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

// serveAPI 启动 HTTP 服务（Hertz）。依赖注入见此处。
func serveAPI() {
	cfg := config.Load()
	gdb, idSvc, sessions, field := initCore(cfg)

	notifSvc := notify.NewService(notify.NewStore(gdb), idSvc, notify.MockSubscribe{})
	schedSvc := schedule.NewService(schedule.NewStore(gdb), idSvc, scheduleNotifier{notifSvc})
	taskSvc := task.NewService(task.NewStore(gdb), idSvc, taskNotifier{notifSvc})
	ingestSvc := newIngest(cfg, gdb, idSvc, schedSvc, notifSvc, field)

	// 拍照导入单张图 ≤5MB，multipart 开销留余量，请求体上限调到 10MB（默认 4MB）。
	h := server.Default(server.WithHostPorts(cfg.APIAddr), server.WithMaxRequestBodySize(10<<20))
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

// serveWorker 异步 Worker（30s 粒度）：触达轮询与升级（ADR-002）、打卡提醒（R3.3）、
// 拍照导入任务接管与残留原图清理（§5.5）；重复日程实例每日续展（ADR-003）。
func serveWorker() {
	cfg := config.Load()
	gdb, idSvc, _, field := initCore(cfg)
	notifSvc := notify.NewService(notify.NewStore(gdb), idSvc, notify.MockSubscribe{})
	schedSvc := schedule.NewService(schedule.NewStore(gdb), idSvc, scheduleNotifier{notifSvc})
	taskSvc := task.NewService(task.NewStore(gdb), idSvc, taskNotifier{notifSvc})
	ingestSvc := newIngest(cfg, gdb, idSvc, schedSvc, notifSvc, field)

	logx.Infof("worker 启动，扫描间隔 30s")
	ctx := context.Background()
	lastExtend := ""
	scan := func() {
		now := time.Now().UTC()
		if err := notifSvc.ProcessDue(ctx); err != nil {
			logx.Errorf("worker 触达扫描失败: %v", err)
		}
		if err := taskSvc.RemindDue(ctx, now); err != nil {
			logx.Errorf("worker 打卡提醒扫描失败: %v", err)
		}
		if err := ingestSvc.RecoverStale(ctx); err != nil {
			logx.Errorf("worker 拍照导入接管失败: %v", err)
		}
		ingestSvc.CleanupOrphans()
		// 续展每天一次（进程启动后首轮即执行一次）。
		if day := now.Format("2006-01-02"); day != lastExtend {
			n, err := schedSvc.ExtendInstances(ctx, now)
			if err != nil {
				logx.Errorf("worker 日程实例续展失败: %v", err)
			} else {
				lastExtend = day
				logx.Infof("worker 日程实例续展完成，新增 %d 条", n)
			}
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
