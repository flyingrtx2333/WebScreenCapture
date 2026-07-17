namespace WebScreenCapture.Client;

public static class ServerAddress
{
    public static string Normalize(string value)
    {
        value = value.Trim().TrimEnd('/');
        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri))
        {
            throw new InvalidOperationException("请输入完整的服务器地址，例如 https://screen.flyingrtx.com。");
        }
        var localDevelopment = uri.IsLoopback && uri.Scheme == Uri.UriSchemeHttp;
        if (uri.Scheme != Uri.UriSchemeHttps && !localDevelopment)
        {
            throw new InvalidOperationException("正式服务器必须使用 HTTPS。");
        }
        return uri.GetLeftPart(UriPartial.Authority);
    }
}
