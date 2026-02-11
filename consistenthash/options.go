package consistenthash

// Option 配置选项
type Option func(*HashRing)

// WithConfig 设置配置
func WithConfig(config *Config) Option {
	return func(r *HashRing) {
		r.config = config
	}
}
