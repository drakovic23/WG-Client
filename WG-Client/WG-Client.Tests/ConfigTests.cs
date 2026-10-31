namespace WG_Client.Tests;

public class ConfigTests
{
    [Theory]
    [InlineData("vpn.example.com", "vpn.example.com")]
    [InlineData("https://vpn.example.com", "vpn.example.com")]
    [InlineData("https://vpn.example.com:12345", "vpn.example.com")]
    [InlineData("vpn.example.com:12345", "vpn.example.com")]
    [InlineData("10.0.0.1", "10.0.0.1")]
    [InlineData("10.0.0.1:12345", "10.0.0.1")]
    public void NormalizeAddress_Pure(string given, string expected)
    {
        string addr = ConfigHandler.NormalizeAddress(given);
        Assert.Equal(expected, addr);
    }

    [Theory]
    [InlineData("")]
    [InlineData("       ")]
    public void NormalizeAddress_Throws_FormatEx(string given)
    {
        Assert.Throws<FormatException>(() => ConfigHandler.NormalizeAddress(given));
    }
}

public class ImportConfigTests : IDisposable
{
    private const string ApiAppId = "11111111-1111-1111-1111-111111111111";
    private const string DelegateAppId = "22222222-2222-2222-2222-222222222222";

    private readonly string _dir =
    Directory.CreateTempSubdirectory("wgclient-tests-").FullName;

    private string WriteConfig(string contents, string name = "test.wgc")
    {
        string path = Path.Combine(_dir, name);
        File.WriteAllText(path, contents);
        return path;
    }

    // A config with every required key, plus any extra lines the test needs
    private string WriteValidConfig(string extraLines = "") => WriteConfig(
        $"""
        server_addr = vpn.example.com
        api_app_id = {ApiAppId}
        delegate_app_id = {DelegateAppId}
        {extraLines}
        """);

    public void Dispose() => Directory.Delete(_dir, recursive: true);

    [Fact]
    public void Defaults_to_https_and_443_when_only_required_keys_are_set()
    {
        var conn = ConfigHandler.ImportConfig(WriteValidConfig(), saveConfig: false);

        Assert.NotNull(conn);
        Assert.Equal("vpn.example.com", conn.Host);
        Assert.Equal("443", conn.Port);
        Assert.True(conn.UseHttps);
    }

    [Fact]
    public void Reads_app_ids_from_config()
    {
        var conn = ConfigHandler.ImportConfig(WriteValidConfig(), saveConfig: false);

        Assert.NotNull(conn);
        Assert.Equal(ApiAppId, conn.M365ApiId);
        Assert.Equal(DelegateAppId, conn.M365DelegateAppId);
    }

    [Theory]
    [InlineData("use_https = false", "80")]
    [InlineData("use_https = FALSE", "80")]
    [InlineData("use_https = yes", "443")]
    public void Port_defaults_follow_use_https(string httpsLine, string expectedPort)
    {
        var conn = ConfigHandler.ImportConfig(WriteValidConfig(httpsLine), saveConfig: false);

        Assert.Equal(expectedPort, conn!.Port);
    }

    [Theory]
    [InlineData("api_app_id = abc")]      // delegate_app_id missing
    [InlineData("delegate_app_id = abc")] // api_app_id missing
    [InlineData("")]                      // both missing
    public void Returns_null_when_an_app_id_is_missing(string idLine)
    {
        var conn = ConfigHandler.ImportConfig(
            WriteConfig($"server_addr = vpn.example.com\n{idLine}"), saveConfig: false);

        Assert.Null(conn);
    }
}
