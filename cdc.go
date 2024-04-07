package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

func runCDC() {
	dbConn, err := pgconn.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}

	err = dbConn.Ping(context.Background())
	if err != nil {
		log.Fatal("failed to ping")
	}

	nc, err := nats.Connect(os.Getenv("NATS_URL"))
	if err != nil {
		log.Fatal(err)
	}

	result := dbConn.Exec(context.Background(), "CREATE PUBLICATION pglogrepl_demo FOR TABLE outbox WITH (publish = 'insert');")
	_, err = result.ReadAll()
	if err != nil {
		log.Fatalln("create publication error", err)
	}

	sysident, err := pglogrepl.IdentifySystem(context.Background(), dbConn)
	if err != nil {
		log.Fatalln("IdentifySystem failed:", err)
	}

	slotName := "pglogrepl_demo"
	_, err = pglogrepl.CreateReplicationSlot(context.Background(), dbConn, slotName, "pgoutput", pglogrepl.CreateReplicationSlotOptions{Temporary: true})
	if err != nil {
		log.Fatalln("CreateReplicationSlot failed:", err)
	}

	err = pglogrepl.StartReplication(context.Background(), dbConn, slotName, sysident.XLogPos, pglogrepl.StartReplicationOptions{PluginArgs: []string{
		"proto_version '2'",
		"publication_names 'pglogrepl_demo'",
		"messages 'true'",
		"streaming 'true'",
	}})
	if err != nil {
		log.Fatalln("StartReplication failed:", err)
	}
	log.Println("Logical replication started on slot", slotName)

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	stopCh := make(chan error, 1)

	go func() {
		<-signalCh
		log.Println("Shutting down...")
		stopCh <- dbConn.Close(context.Background())
	}()

	go func() {
		clientXLogPos := sysident.XLogPos
		standbyMessageTimeout := time.Second * 10
		nextStandbyMessageDeadline := time.Now().Add(standbyMessageTimeout)
		relationsV2 := map[uint32]*pglogrepl.RelationMessageV2{}
		typeMap := pgtype.NewMap()
		inStream := false

		for {
			if time.Now().After(nextStandbyMessageDeadline) {
				err = pglogrepl.SendStandbyStatusUpdate(context.Background(), dbConn, pglogrepl.StandbyStatusUpdate{WALWritePosition: clientXLogPos})
				if err != nil {
					log.Fatalln("SendStandbyStatusUpdate failed:", err)
				}

				nextStandbyMessageDeadline = time.Now().Add(standbyMessageTimeout)
			}

			ctx, cancel := context.WithDeadline(context.Background(), nextStandbyMessageDeadline)
			rawMsg, err := dbConn.ReceiveMessage(ctx)
			cancel()
			if err != nil {
				if pgconn.Timeout(err) {
					continue
				}

				var pgConnErr *pgconn.ConnectError
				if errors.As(err, &pgConnErr) {
					return
				}

				log.Fatalln("ReceiveMessage failed:", err)
			}

			if errMsg, ok := rawMsg.(*pgproto3.ErrorResponse); ok {
				log.Fatalf("received Postgres WAL error: %+v", errMsg)
			}

			msg, ok := rawMsg.(*pgproto3.CopyData)
			if !ok {
				log.Printf("Received unexpected message: %T\n", rawMsg)
				continue
			}

			switch msg.Data[0] {
			case pglogrepl.PrimaryKeepaliveMessageByteID:
				pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:])
				if err != nil {
					log.Fatalln("ParsePrimaryKeepaliveMessage failed:", err)
				}
				if pkm.ServerWALEnd > clientXLogPos {
					clientXLogPos = pkm.ServerWALEnd
				}
				if pkm.ReplyRequested {
					nextStandbyMessageDeadline = time.Time{}
				}

			case pglogrepl.XLogDataByteID:
				xld, err := pglogrepl.ParseXLogData(msg.Data[1:])
				if err != nil {
					log.Fatalln("ParseXLogData failed:", err)
				}

				processV2(nc, xld.WALData, relationsV2, typeMap, &inStream)

				if xld.WALStart > clientXLogPos {
					clientXLogPos = xld.WALStart
				}
			}
		}
	}()

	<-stopCh
	log.Println("CDC stopped")
}

func processV2(nc *nats.Conn, walData []byte, relations map[uint32]*pglogrepl.RelationMessageV2, typeMap *pgtype.Map, inStream *bool) {
	logicalMsg, err := pglogrepl.ParseV2(walData, *inStream)
	if err != nil {
		log.Fatalf("Parse logical replication message: %s", err)
	}

	switch logicalMsg := logicalMsg.(type) {
	case *pglogrepl.RelationMessageV2:
		relations[logicalMsg.RelationID] = logicalMsg
	case *pglogrepl.InsertMessageV2:
		rel, ok := relations[logicalMsg.RelationID]
		if !ok {
			log.Fatalf("unknown relation ID %d", logicalMsg.RelationID)
		}
		values := map[string]interface{}{}
		for idx, col := range logicalMsg.Tuple.Columns {
			colName := rel.Columns[idx].Name
			switch col.DataType {
			case 't': // text
				val, err := decodeTextColumnData(typeMap, col.Data, rel.Columns[idx].DataType)
				if err != nil {
					log.Fatalln("error decoding column data:", err)
				}
				values[colName] = val
			}
		}
		b, err := json.Marshal(values)
		if err != nil {
			log.Println("failed to marshal json", err)
			return
		}

		err = nc.Publish("user.created", b)
		if err != nil {
			log.Println("failed to publish message", err)
		}

		log.Println("sucessfully publish message with ", values)

	case *pglogrepl.StreamStartMessageV2:
		*inStream = true
	case *pglogrepl.StreamStopMessageV2:
		*inStream = false
	default:
		// no action
	}
}

func decodeTextColumnData(mi *pgtype.Map, data []byte, dataType uint32) (interface{}, error) {
	if dt, ok := mi.TypeForOID(dataType); ok {
		return dt.Codec.DecodeValue(mi, dataType, pgtype.TextFormatCode, data)
	}
	return string(data), nil
}
