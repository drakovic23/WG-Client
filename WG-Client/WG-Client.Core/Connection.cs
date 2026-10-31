using System.ComponentModel;

namespace WG_Client.Core
{
    public enum ConnectionState : byte
    {
        NotAttempted,
        ErrorConnecting,
        Connecting,
        Connected,
        Disconnected,
    }
    public class Connection : INotifyPropertyChanged
    {
        public string Host { get; private set; }
        public string Port { get; private set; }
        public bool UseHttps { get; private set; }
        public string M365ApiId { get; private set; }
        public string M365DelegateAppId { get; private set; }

        public Connection(string host, string port, bool useHttps)
        {
            Host = host;
            Port = port;
            UseHttps = useHttps;
        }

        public Connection(string host, string port, bool useHttps, string apiAppId, string delegateAppId)
        {
            Host = host;
            Port = port;
            UseHttps = useHttps;
            M365ApiId = apiAppId;
            M365DelegateAppId = delegateAppId;
        }

        private ConnectionState _state;
        public ConnectionState State
        {
            get => _state;
            set
            {
                if (_state == value) return;
                _state = value;
                PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(State)));
                PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(IsConnecting)));
                PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(HasConnected)));
                PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(ErrorConnecting)));
                PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(Disconnected)));

            }
        }
        public bool IsConnecting => State == ConnectionState.Connecting;
        public bool HasConnected => State == ConnectionState.Connected;
        public bool ErrorConnecting => State == ConnectionState.ErrorConnecting;
        public bool Disconnected => State == ConnectionState.Disconnected;


        public event PropertyChangedEventHandler? PropertyChanged;
    }
}
