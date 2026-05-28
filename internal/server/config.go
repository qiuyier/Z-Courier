package server

type Config struct {
	RouteMsgIDs []uint32
}

func DefaultConfig() Config {
	return Config{
		RouteMsgIDs: []uint32{1000},
	}
}
