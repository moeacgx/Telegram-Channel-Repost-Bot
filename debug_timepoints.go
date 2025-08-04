package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	// 可能的数据库文件路径
	possiblePaths := []string{
		"data/bot.db",
		"bot.db",
		"./data/bot.db",
		"./bot.db",
	}
	
	var dbPath string
	var found bool
	
	// 查找数据库文件
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			dbPath = path
			found = true
			fmt.Printf("找到数据库文件: %s\n", path)
			break
		}
	}
	
	if !found {
		log.Fatalf("数据库文件不存在")
	}

	// 连接数据库
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.Close()

	fmt.Println("🔍 检查时间点模式配置...")
	
	// 查询时间点模式的频道组
	rows, err := db.Query(`
		SELECT id, name, schedule_mode, schedule_timepoints, is_active 
		FROM channel_groups 
		WHERE schedule_mode = 'timepoints'
	`)
	if err != nil {
		log.Fatalf("查询失败: %v", err)
	}
	defer rows.Close()

	fmt.Println("\n📋 时间点模式的频道组:")
	fmt.Println("ID | Name | Active | Timepoints")
	fmt.Println("---|------|--------|----------")
	
	var hasTimepointGroups bool
	for rows.Next() {
		hasTimepointGroups = true
		var id int64
		var name, scheduleMode string
		var scheduleTimepoints sql.NullString
		var isActive bool
		
		err := rows.Scan(&id, &name, &scheduleMode, &scheduleTimepoints, &isActive)
		if err != nil {
			log.Printf("扫描行失败: %v", err)
			continue
		}
		
		timepointsValue := "NULL"
		if scheduleTimepoints.Valid {
			timepointsValue = scheduleTimepoints.String
		}
		
		activeStatus := "❌"
		if isActive {
			activeStatus = "✅"
		}
		
		fmt.Printf("%d | %s | %s | %s\n", id, name, activeStatus, timepointsValue)
	}
	
	if !hasTimepointGroups {
		fmt.Println("❌ 没有找到时间点模式的频道组")
		return
	}
	
	// 检查当前时间
	now := time.Now()
	fmt.Printf("\n🕐 当前时间: %s\n", now.Format("2006-01-02 15:04:05"))
	fmt.Printf("当前小时: %d, 当前分钟: %d\n", now.Hour(), now.Minute())
	
	// 检查最近的发送记录
	fmt.Println("\n📊 最近的发送记录:")
	recordRows, err := db.Query(`
		SELECT sr.group_id, cg.name, sr.message_type, sr.status, sr.scheduled_at, sr.sent_at, sr.error_message
		FROM send_records sr
		JOIN channel_groups cg ON sr.group_id = cg.id
		WHERE cg.schedule_mode = 'timepoints'
		ORDER BY sr.created_at DESC
		LIMIT 10
	`)
	if err != nil {
		log.Printf("查询发送记录失败: %v", err)
		return
	}
	defer recordRows.Close()
	
	fmt.Println("Group | Type | Status | Scheduled | Sent | Error")
	fmt.Println("------|------|--------|-----------|------|------")
	
	for recordRows.Next() {
		var groupID int64
		var groupName, messageType, status string
		var scheduledAt, sentAt sql.NullString
		var errorMessage sql.NullString
		
		err := recordRows.Scan(&groupID, &groupName, &messageType, &status, &scheduledAt, &sentAt, &errorMessage)
		if err != nil {
			log.Printf("扫描记录失败: %v", err)
			continue
		}
		
		scheduledStr := "NULL"
		if scheduledAt.Valid {
			scheduledStr = scheduledAt.String
		}
		
		sentStr := "NULL"
		if sentAt.Valid {
			sentStr = sentAt.String
		}
		
		errorStr := ""
		if errorMessage.Valid {
			errorStr = errorMessage.String
		}
		
		fmt.Printf("%s | %s | %s | %s | %s | %s\n", 
			groupName, messageType, status, scheduledStr, sentStr, errorStr)
	}
	
	// 检查是否有消息模板
	fmt.Println("\n📝 检查消息模板:")
	templateRows, err := db.Query(`
		SELECT cg.id, cg.name, cg.message_id, mt.title, mt.content
		FROM channel_groups cg
		LEFT JOIN message_templates mt ON cg.message_id = mt.id
		WHERE cg.schedule_mode = 'timepoints'
	`)
	if err != nil {
		log.Printf("查询模板失败: %v", err)
		return
	}
	defer templateRows.Close()
	
	fmt.Println("Group | Template ID | Template Title | Has Content")
	fmt.Println("------|-------------|----------------|------------")
	
	for templateRows.Next() {
		var groupID, messageID int64
		var groupName string
		var templateTitle, templateContent sql.NullString
		
		err := templateRows.Scan(&groupID, &groupName, &messageID, &templateTitle, &templateContent)
		if err != nil {
			log.Printf("扫描模板失败: %v", err)
			continue
		}
		
		titleStr := "NULL"
		if templateTitle.Valid {
			titleStr = templateTitle.String
		}
		
		hasContent := "❌"
		if templateContent.Valid && templateContent.String != "" {
			hasContent = "✅"
		}
		
		fmt.Printf("%s | %d | %s | %s\n", groupName, messageID, titleStr, hasContent)
	}
}
