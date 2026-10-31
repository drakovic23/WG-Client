using System.Diagnostics;
using WG_Client.Core;

namespace WG_Client
{
    public static class ConfigHandler
    {
        public static Connection? ImportConfig(string path, bool saveConfig = true) // Needs better handling
        {
            Debug.WriteLine($"ConfigHandler: Importing config at {path}");

            if (!File.Exists(path))
                return null;

            var values = File.ReadLines(path)
            .Select(l => l.Trim())
            .Where(l => l.Length > 0 && !l.StartsWith('#') && l.Contains('='))
            .Select(l => l.Split('=', 2))
            .ToDictionary(p => p[0].Trim(), p => p[1].Trim().Trim('"'));

            if (!values.ContainsKey("server_addr"))
                return null;

            string normalized = "";

            try
            {
                normalized = NormalizeAddress(values["server_addr"]);
            }
            catch(Exception ex)
            {
                Debug.WriteLine($"Error parsing config: {ex.Message}");
                return null;
            }

            bool useHttps = true;
            if (values.ContainsKey("use_https"))
            {
                if (bool.TryParse(values["use_https"], out bool https))
                    useHttps = https;
            }

            string srvPort = "";
            if (values.ContainsKey("server_port") && values["server_port"].Length > 0)
            {
                if (int.TryParse(values["server_port"], out int port))
                {
                    srvPort = values["server_port"];
                }
            }
            else // No port was specified
            {
                srvPort = useHttps ? "443" : "80";
            }

            if(!values.ContainsKey("api_app_id") || !values.ContainsKey("delegate_app_id"))
            {
                return null;
            }

            if (saveConfig)
            {
                string dataPath = ResolveDataStorePath();
                string configPath = Path.Combine(dataPath, normalized + ".wgc");

                Debug.WriteLine($"Storing config path at {configPath}");
                File.Copy(path, configPath, true);
            }

            return new Connection (normalized, srvPort , useHttps, values["api_app_id"], values["delegate_app_id"]);
        }

        public static List<Connection> LoadSavedConfigs()
        {
            string dataPath = ResolveDataStorePath();
            string[] files = Directory.GetFiles(dataPath, "*.wgc");

            List<Connection> connections = new();
            foreach(var file in files)
            {
                Connection? newCon = ImportConfig(file, false);

                if(newCon != null)
                {
                    connections.Add(newCon);
                }
            }

            return connections;
        }

        public static string NormalizeAddress(string address)
        {
            string s = address.Trim().Trim('"');

            if (!s.Contains("://"))
                s = "http://" + s;

            if (!Uri.TryCreate(s, UriKind.Absolute, out Uri? uri))
                throw new FormatException($"Invalid server_addr: '{address}'");

            return uri.Host; // just "someaddress"
        }

#if DEBUG
        public const string DATA_FOLDER_NAME = "Deni.Dev";
#else
        public const string DATA_FOLDER_NAME = "Deni";
#endif
        public static string ResolveDataStorePath()
        {
            return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), DATA_FOLDER_NAME);
        }

        public static void EnsureDataPathExists()
        {
            var dir = Directory.CreateDirectory(ResolveDataStorePath());
            Debug.WriteLine($"Data directory (app view): {dir.FullName}");


        }
        public static void ReadConfig(string connectionName)
        {

        }
    }
}
