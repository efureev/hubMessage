package hub

type AppModuleConfig interface {
	Name() string
	Version() string
}

type AppModuleEvent *func(arg ...interface{})

type AppModule interface {
	SetConfig(config AppModuleConfig)
	Config() AppModuleConfig

	//Init(config AppModuleConfig) error
	Init() error
	//Start() error
	Destroy() error

	//Get() AppModule

	//BeforeStart(fn AppModuleEvent) error
	BeforeStart(fn func(mod AppModule) error)
	//AfterStart(func(arg ...interface{})) error
	//BeforeDestroy(func(arg ...interface{})) error
	//AfterDestroy(func(arg ...interface{})) error
}

type Config struct {
	name    string
	version string
}

func (c Config) Name() string {
	return c.name
}

func (c Config) Version() string {
	return c.version
}

type BaseAppModule struct {
	config AppModuleConfig
	//beforeStartFn *AppModuleEvent
	//beforeStartFn AppModuleEvent
	beforeStartFn func(mod AppModule) error
	//beforeStartFn func(mod AppModule, arg ...interface{})
}

func (h BaseAppModule) Config() AppModuleConfig {
	return h.config
}

func (h *BaseAppModule) SetConfig(config AppModuleConfig) {
	h.config = config
}

//var instance AppModule

/*func (b BaseAppModule) Get() AppModule {
	return instance
}
*/
/*func (b BaseAppModule) Init(config AppModuleConfig) error {
	return nil
}
*/

func (b *BaseAppModule) Init() error {
	if b.beforeStartFn != nil {
		if err := b.beforeStartFn(b); err != nil {
			panic(err)
		}
	}
	//instance = b
	return nil
}

/*
func (b BaseAppModule) Start() error {
	return nil
}*/
func (b BaseAppModule) Destroy() error {
	return nil
}

/*func Start(config AppModuleConfig) error {
	return New(config)
}
*/

//func (b *BaseAppModule) BeforeStart(fn func(mod AppModule, arg ...interface{})) error {
func (b *BaseAppModule) BeforeStart(fn func(mod AppModule) error) {
	//func (b *BaseAppModule) BeforeStart(fn AppModuleEvent) error {
	b.beforeStartFn = fn
}

func DefaultConfig() AppModuleConfig {
	return Config{`App Module`, `v0.0.1`}
}
