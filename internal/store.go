package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // SQLite 驱动
)

// op 定义数据库事务操作函数类型
type op func(*sql.Tx) error

// Store 管理应用程序的持久化存储
// 使用 SQLite 数据库存储桥接配置、消息映射和用户映射
type Store struct {
	db    *sql.DB        // 数据库连接
	cache sync.Map       // 内存缓存，减少数据库查询
	ops   chan op        // 异步写入队列
	stop  chan struct{}  // 停止信号
	wg    sync.WaitGroup // 等待写入协程完成
}

// NewStore 创建新的存储实例
// 参数:
//   - path: SQLite 数据库文件路径
//
// 返回:
//   - *Store: 新的存储实例
//   - error: 初始化过程中的错误
func NewStore(path string) (*Store, error) {
	// 配置 SQLite 连接参数
	// WAL: 预写式日志模式，提高并发性能
	// timeout: 锁等待超时时间
	// NORMAL: 同步模式，平衡性能和安全性
	dsn := fmt.Sprintf("%s?_journal=WAL&_timeout=5000&_sync=NORMAL", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// 创建数据库表结构
	sqls := []string{
		// 桥接组表
		`CREATE TABLE IF NOT EXISTS groups (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		// 绑定表：存储桥接组中的各个节点
		`CREATE TABLE IF NOT EXISTS binds (bind_id INTEGER, plat TEXT, room TEXT, cfg TEXT, PRIMARY KEY (plat, room))`,
		`CREATE INDEX IF NOT EXISTS idx_bind_group ON binds(bind_id)`,
		// 消息映射表：跟踪跨平台的消息对应关系
		`CREATE TABLE IF NOT EXISTS maps (src_plat TEXT, src_msg TEXT, dst_plat TEXT, dst_msg TEXT, bind_id INTEGER, ts INTEGER, PRIMARY KEY (src_plat, src_msg, dst_plat))`,
		`CREATE INDEX IF NOT EXISTS idx_map_ts ON maps(ts)`, // 用于过期数据清理
		// 用户映射表：存储跨平台的用户身份映射
		`CREATE TABLE IF NOT EXISTS users (src_plat TEXT, src_user TEXT, dst_plat TEXT, dst_user TEXT, PRIMARY KEY (src_plat, src_user, dst_plat))`,
	}

	// 执行建表语句
	for _, q := range sqls {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}

	s := &Store{
		db:   db,
		ops:  make(chan op, 2000), // 写入队列缓冲区
		stop: make(chan struct{}),
	}
	s.wg.Add(1)

	// 启动后台协程
	go s.writer()  // 批量写入协程
	go s.cleaner() // 数据清理协程

	return s, nil
}

// Close 关闭存储，等待所有待处理的写入操作完成
// 返回:
//   - error: 关闭过程中的错误
func (s *Store) Close() error {
	close(s.stop) // 发送停止信号
	s.wg.Wait()   // 等待写入协程完成

	if err := s.db.Close(); err != nil {
		return err
	}
	return nil
}

// writer 后台批量写入协程
// 定期批量提交写入操作，提高数据库性能
func (s *Store) writer() {
	defer s.wg.Done()

	t := time.NewTicker(200 * time.Millisecond) // 每 200ms 批量提交一次
	defer t.Stop()

	batch := make([]op, 0, 100) // 预分配缓冲区

	// exec 执行批量写入
	exec := func() {
		if len(batch) == 0 {
			return
		}

		tx, err := s.db.Begin() // 开始事务
		if err != nil {
			batch = batch[:0] // 重置切片但保留容量
			return
		}

		// 执行所有批量操作
		for _, fn := range batch {
			_ = fn(tx) // 忽略单个操作的错误
		}

		_ = tx.Commit()   // 提交事务
		batch = batch[:0] // 重置切片但保留容量
	}

	for {
		select {
		case fn := <-s.ops:
			// 收到写入操作
			batch = append(batch, fn)
			if len(batch) >= 100 {
				exec() // 达到批量大小，立即提交
			}
		case <-t.C:
			// 定时器触发，提交当前批次
			exec()
		case <-s.stop:
			// 收到停止信号，提交剩余操作并退出
			exec()
			return
		}
	}
}

// push 将写入操作添加到队列
// 如果队列已满，则丢弃操作（非阻塞）
func (s *Store) push(fn op) {
	select {
	case s.ops <- fn:
	default: // 队列满时不阻塞
	}
}

// Map 保存消息在不同平台间的映射关系
// 异步写入，不阻塞调用者
// 参数:
//   - sPlat: 源平台名称
//   - sMsg: 源消息 ID
//   - dPlat: 目标平台名称
//   - dMsg: 目标消息 ID
//   - bid: 桥接组 ID
func (s *Store) Map(sPlat, sMsg, dPlat, dMsg string, bid int64) {
	s.push(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT OR IGNORE INTO maps (src_plat, src_msg, dst_plat, dst_msg, bind_id, ts) VALUES (?, ?, ?, ?, ?, ?)",
			sPlat, sMsg, dPlat, dMsg, bid, time.Now().Unix())
		return err
	})
}

// Seek 查找消息在目标平台的 ID
// 参数:
//   - sPlat: 源平台名称
//   - sMsg: 源消息 ID
//   - dPlat: 目标平台名称
//
// 返回:
//   - string: 目标平台的消息 ID
//   - bool: 是否找到映射
func (s *Store) Seek(sPlat, sMsg, dPlat string) (string, bool) {
	var dMsg string
	err := s.db.QueryRow("SELECT dst_msg FROM maps WHERE src_plat=? AND src_msg=? AND dst_plat=?", sPlat, sMsg, dPlat).Scan(&dMsg)
	return dMsg, err == nil
}

// Echo 检查消息是否为回声（由本系统发送到该平台）
// 用于避免消息循环
// 参数:
//   - plat: 平台名称
//   - msg: 消息 ID
//
// 返回:
//   - bool: 是否为回声消息
func (s *Store) Echo(plat, msg string) bool {
	var i int
	return s.db.QueryRow("SELECT 1 FROM maps WHERE dst_plat=? AND dst_msg=? LIMIT 1", plat, msg).Scan(&i) == nil
}

// BindUser 绑定用户在不同平台间的映射关系
// 参数:
//   - sPlat: 源平台名称
//   - sUser: 源用户 ID
//   - dPlat: 目标平台名称
//   - dUser: 目标用户 ID
//
// 返回:
//   - error: 绑定过程中的错误
func (s *Store) BindUser(sPlat, sUser, dPlat, dUser string) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO users (src_plat, src_user, dst_plat, dst_user) VALUES (?, ?, ?, ?)", sPlat, sUser, dPlat, dUser)
	if err == nil {
		// 清除缓存 - 使用 strings.Builder 构建键
		var keyBuilder strings.Builder
		keyBuilder.Grow(len(sPlat) + len(sUser) + len(dPlat) + 4)
		keyBuilder.WriteString("u:")
		keyBuilder.WriteString(sPlat)
		keyBuilder.WriteByte(':')
		keyBuilder.WriteString(sUser)
		keyBuilder.WriteByte(':')
		keyBuilder.WriteString(dPlat)
		s.cache.Delete(keyBuilder.String())
	}
	return err
}

// FindUser 查找用户在目标平台的 ID
// 使用缓存加速查询
// 参数:
//   - sPlat: 源平台名称
//   - sUser: 源用户 ID
//   - dPlat: 目标平台名称
//
// 返回:
//   - string: 目标平台的用户 ID
//   - bool: 是否找到映射
func (s *Store) FindUser(sPlat, sUser, dPlat string) (string, bool) {
	// 使用 strings.Builder 构建缓存键
	var keyBuilder strings.Builder
	keyBuilder.Grow(len(sPlat) + len(sUser) + len(dPlat) + 4) // 预分配容量: "u:" + 2个冒号
	keyBuilder.WriteString("u:")
	keyBuilder.WriteString(sPlat)
	keyBuilder.WriteByte(':')
	keyBuilder.WriteString(sUser)
	keyBuilder.WriteByte(':')
	keyBuilder.WriteString(dPlat)
	k := keyBuilder.String()

	// 先查缓存
	if v, ok := s.cache.Load(k); ok {
		return v.(string), true
	}

	// 查数据库
	var dUser string
	err := s.db.QueryRow("SELECT dst_user FROM users WHERE src_plat=? AND src_user=? AND dst_plat=?", sPlat, sUser, dPlat).Scan(&dUser)
	if err == nil {
		s.cache.Store(k, dUser) // 写入缓存
		return dUser, true
	}
	return "", false
}

// TargetRoom 查找源房间在目标平台对应的房间 ID
// 通过桥接配置查找
// 参数:
//   - sPlat: 源平台名称
//   - sRoom: 源房间 ID
//   - dPlat: 目标平台名称
//
// 返回:
//   - string: 目标平台的房间 ID
//   - bool: 是否找到映射
func (s *Store) TargetRoom(sPlat, sRoom, dPlat string) (string, bool) {
	query := `
		SELECT t.room 
		FROM binds AS s
		JOIN binds AS t ON s.bind_id = t.bind_id
		WHERE s.plat = ? AND s.room = ? AND t.plat = ?
	`
	var dRoom string
	err := s.db.QueryRow(query, sPlat, sRoom, dPlat).Scan(&dRoom)
	return dRoom, err == nil
}

// Find 查找房间所属的桥接组
// 使用缓存加速查询
// 参数:
//   - plat: 平台名称
//   - room: 房间 ID
//
// 返回:
//   - []*Group: 桥接组列表（通常只有一个）
func (s *Store) Find(plat, room string) []*Group {
	// 使用 strings.Builder 构建缓存键，减少字符串拼接开销
	var keyBuilder strings.Builder
	keyBuilder.Grow(len(plat) + len(room) + 1) // 预分配容量
	keyBuilder.WriteString(plat)
	keyBuilder.WriteByte(':')
	keyBuilder.WriteString(room)
	k := keyBuilder.String()

	// 先查缓存
	if v, ok := s.cache.Load(k); ok {
		return v.([]*Group)
	}

	// 查询桥接组 ID
	var bid int64
	err := s.db.QueryRow("SELECT bind_id FROM binds WHERE plat=? AND room=?", plat, room).Scan(&bid)
	if err != nil {
		return []*Group{} // 未找到桥接
	}

	// 获取桥接组名称
	var name string
	_ = s.db.QueryRow("SELECT name FROM groups WHERE id=?", bid).Scan(&name)

	// 查询桥接组中的所有节点
	rows, err := s.db.Query("SELECT plat, room, cfg FROM binds WHERE bind_id=?", bid)
	if err != nil {
		return []*Group{}
	}
	defer rows.Close()

	g := &Group{
		ID:    bid,
		Name:  name,
		Nodes: make([]Node, 0, 4), // 预分配节点切片，通常桥接不会超过4个平台
	}

	for rows.Next() {
		var p, r, cStr string
		if rows.Scan(&p, &r, &cStr) == nil {
			node := Node{Plat: p, Room: r}
			if cStr != "" {
				_ = json.Unmarshal([]byte(cStr), &node.Cfg) // 反序列化配置
			}
			g.Nodes = append(g.Nodes, node)
		}
	}

	res := []*Group{g}
	s.cache.Store(k, res) // 写入缓存
	return res
}

// Add 创建新的桥接组
// 参数:
//   - name: 桥接组名称
//   - nodes: 桥接的节点列表
//
// 返回:
//   - *Group: 创建的桥接组
//   - error: 创建过程中的错误
func (s *Store) Add(name string, nodes []Node) (*Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // 如果提交失败则回滚

	// 插入桥接组
	res, err := tx.Exec("INSERT INTO groups (name) VALUES (?)", name)
	if err != nil {
		return nil, err
	}
	bid, err := res.LastInsertId() // 获取自增 ID
	if err != nil {
		return nil, err
	}

	// 插入所有节点
	for _, n := range nodes {
		bs, _ := json.Marshal(n.Cfg) // 序列化配置
		if _, err := tx.Exec("INSERT INTO binds (bind_id, plat, room, cfg) VALUES (?, ?, ?, ?)", bid, n.Plat, n.Room, string(bs)); err != nil {
			return nil, err
		}
		// 清除节点的缓存 - 使用 strings.Builder
		var keyBuilder strings.Builder
		keyBuilder.Grow(len(n.Plat) + len(n.Room) + 1)
		keyBuilder.WriteString(n.Plat)
		keyBuilder.WriteByte(':')
		keyBuilder.WriteString(n.Room)
		s.cache.Delete(keyBuilder.String())
	}

	if err := tx.Commit(); err != nil { // 提交事务
		return nil, err
	}
	return &Group{ID: bid, Name: name, Nodes: nodes}, nil
}

// cleaner 后台清理协程
// 定期清理过期的消息映射数据（超过 48 小时）
func (s *Store) cleaner() {
	t := time.NewTicker(12 * time.Hour) // 每 12 小时清理一次
	for range t.C {
		exp := time.Now().Add(-48 * time.Hour).Unix() // 48 小时前的时间戳
		s.push(func(tx *sql.Tx) error {
			_, err := tx.Exec("DELETE FROM maps WHERE ts < ?", exp)
			return err
		})
	}
}
