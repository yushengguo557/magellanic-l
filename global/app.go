package global

import (
	"database/sql"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/yushengguo557/magellanic-l/config"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

var App = new(Application)

type Application struct {
	Config  config.Configuration
	Log     *zap.Logger
	Engine  *gin.Engine
	MongoDB *mongo.Client
	TiDB    *sql.DB
	Redis   *redis.Client
}
