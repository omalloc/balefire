package conf

type Bootstrap struct {
	Logging   Logging   `yaml:"logging" json:"logging"`
	Transport Transport `yaml:"transport" json:"transport"`
	Storage   Storage   `yaml:"storage" json:"storage"`
}

type Logging struct {
	Level  string `yaml:"level" json:"level"`
	Caller bool   `yaml:"caller" json:"caller"`
}

type Transport struct {
	Mode        string   `yaml:"mode" json:"mode"`
	PrivateKey  string   `yaml:"private_key" json:"private_key"`
	PublicKey   string   `yaml:"public_key" json:"public_key"`
	ListenAddrs []string `yaml:"listen_addrs" json:"listen_addrs"`
	Peers       []string `yaml:"peers" json:"peers"`
}

type Storage struct {
	Path string `yaml:"path" json:"path"`
}
