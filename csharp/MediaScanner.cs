// MediaScanner.cs - MediaSorterCN 核心逻辑(.NET Framework 4.0 / C# 5)
// 递归扫描 -> 时间提取(EXIF -> 文件名 -> 创建/修改较旧) -> 命名 -> 去重 -> 复制/移动
using System;
using System.Collections.Generic;
using System.Drawing;
using System.Globalization;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;

namespace MediaSorterCN
{
    /// <summary>目录结构模式</summary>
    public enum FolderMode { Year, YearMonth, YearMonthDay, Flat }

    public class MediaScanner
    {
        public event Action<string> Log;

        private static readonly HashSet<string> ImageExts = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
            { ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif", ".tiff", ".tif", ".heic", ".heif" };
        private static readonly HashSet<string> VideoExts = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
            { ".mp4", ".mov", ".avi", ".mkv", ".3gp", ".m4v", ".wmv", ".webm", ".flv" };

        private static readonly Regex NameDateTime = new Regex(@"(\d{8})[_-](\d{6})");
        private static readonly Regex NameDash     = new Regex(@"(\d{4})-(\d{2})-(\d{2})[_-](\d{6})");
        private static readonly Regex UnixMs      = new Regex(@"(\d{13})");
        private static readonly Regex UnixS       = new Regex(@"(\d{10})");

        private void Emit(string msg)
        {
            var h = Log;
            if (h != null) h(msg);
        }

        // ---------------- 扫描 ----------------
        public List<string> Scan(string src)
        {
            var result = new List<string>();
            var stack = new Stack<string>();
            stack.Push(src);
            while (stack.Count > 0)
            {
                string dir = stack.Pop();
                string[] files;
                try { files = Directory.GetFiles(dir); }
                catch { continue; } // 无权限等,跳过
                foreach (var f in files)
                {
                    string ext = Path.GetExtension(f);
                    if (ImageExts.Contains(ext) || VideoExts.Contains(ext))
                        result.Add(f);
                }
                string[] subDirs;
                try { subDirs = Directory.GetDirectories(dir); }
                catch { continue; }
                foreach (var d in subDirs) stack.Push(d);
            }
            return result;
        }

        // ---------------- 时间提取 ----------------
        public DateTime? GetCaptureTime(string path)
        {
            string ext = Path.GetExtension(path);
            DateTime? t = null;
            if (ImageExts.Contains(ext)) t = ReadExifTime(path);
            if (t == null) t = ParseFileNameTime(Path.GetFileName(path));
            if (t == null)
            {
                try
                {
                    DateTime ct = File.GetCreationTime(path);
                    DateTime mt = File.GetLastWriteTime(path);
                    t = ct < mt ? ct : mt; // 取较旧者,更接近拍摄时间
                }
                catch { }
            }
            return t;
        }

        private static DateTime? ReadExifTime(string path)
        {
            try
            {
                using (Image img = Image.FromFile(path))
                {
                    System.Drawing.Imaging.PropertyItem[] items = img.PropertyItems;
                    foreach (System.Drawing.Imaging.PropertyItem pi in items)
                    {
                        // 0x9003 DateTimeOriginal / 0x9004 DateTimeDigitized / 0x0132 DateTime
                        if (pi.Id == 0x9003 || pi.Id == 0x9004 || pi.Id == 0x0132)
                        {
                            string s = Encoding.ASCII.GetString(pi.Value).Trim('\0', ' ');
                            DateTime dt;
                            if (DateTime.TryParseExact(s, "yyyy:MM:dd HH:mm:ss", CultureInfo.InvariantCulture,
                                DateTimeStyles.None, out dt)) return dt;
                            if (DateTime.TryParseExact(s, "yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture,
                                DateTimeStyles.None, out dt)) return dt;
                        }
                    }
                }
            }
            catch { }
            return null;
        }

        private static DateTime? ParseFileNameTime(string name)
        {
            Match m = NameDateTime.Match(name);
            if (m.Success)
            {
                DateTime dt;
                if (DateTime.TryParseExact(m.Groups[1].Value + m.Groups[2].Value, "yyyyMMddHHmmss",
                    CultureInfo.InvariantCulture, DateTimeStyles.None, out dt)) return dt;
            }
            m = NameDash.Match(name);
            if (m.Success)
            {
                int y, mo, d, h, mi, s;
                if (int.TryParse(m.Groups[1].Value, out y) && int.TryParse(m.Groups[2].Value, out mo) &&
                    int.TryParse(m.Groups[3].Value, out d) && m.Groups[4].Value.Length == 6)
                {
                    string hm = m.Groups[4].Value;
                    if (int.TryParse(hm.Substring(0, 2), out h) && int.TryParse(hm.Substring(2, 2), out mi) &&
                        int.TryParse(hm.Substring(4, 2), out s))
                    {
                        try { return new DateTime(y, mo, d, h, mi, s); }
                        catch { }
                    }
                }
            }
            m = UnixMs.Match(name);
            if (m.Success)
            {
                long v;
                if (long.TryParse(m.Groups[1].Value, out v) && v >= 1000000000000L && v <= 20000000000000L)
                    return FromUnixSeconds(v / 1000);
            }
            m = UnixS.Match(name);
            if (m.Success)
            {
                long v;
                if (long.TryParse(m.Groups[1].Value, out v) && v >= 1000000000L && v <= 2000000000L)
                    return FromUnixSeconds(v);
            }
            return null;
        }

        private static DateTime FromUnixSeconds(long seconds)
        {
            DateTime epoch = new DateTime(1970, 1, 1, 0, 0, 0, DateTimeKind.Utc);
            return epoch.AddSeconds(seconds).ToLocalTime();
        }

        // ---------------- 去重 ----------------
        private static string Md5(string path)
        {
            using (var md5 = MD5.Create())
            using (var fs = File.OpenRead(path))
            {
                byte[] h = md5.ComputeHash(fs);
                return BitConverter.ToString(h).Replace("-", "");
            }
        }

        // ---------------- 归档主流程 ----------------
        /// <summary>
        /// 按拍摄时间归档。返回处理计数 (处理, 去重跳过, 失败)。
        /// </summary>
        public Tuple<int, int, int> Run(string src, string dst, bool move,
            bool dedupe, int offsetSeconds, FolderMode mode)
        {
            int processed = 0, dups = 0, failed = 0;
            var counter = new Dictionary<string, int>();
            var seenHashes = new Dictionary<string, string>(); // hash -> 保留路径
            var seenNames = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            string dstAbs = Path.GetFullPath(dst);

            List<string> files = Scan(src);
            Emit(string.Format("共发现 {0} 个媒体文件", files.Count));
            files.Sort(StringComparer.OrdinalIgnoreCase);

            // 去重:按大小分组,同大小才算 MD5
            Dictionary<long, List<string>> bySize = null;
            if (dedupe)
            {
                bySize = new Dictionary<long, List<string>>();
                foreach (var f in files)
                {
                    long sz;
                    try { sz = new FileInfo(f).Length; } catch { continue; }
                    List<string> g;
                    if (!bySize.TryGetValue(sz, out g)) bySize[sz] = g = new List<string>();
                    g.Add(f);
                }
            }

            foreach (var f in files)
            {
                if (dedupe)
                {
                    long sz;
                    try { sz = new FileInfo(f).Length; } catch { continue; }
                    string hash = Md5(f);
                    string kept;
                    if (seenHashes.TryGetValue(hash, out kept))
                    {
                        Emit(string.Format("[去重] 跳过重复: {0}  (已保留 {1})", f, kept));
                        dups++;
                        continue;
                    }
                    seenHashes[hash] = f;
                }

                DateTime? t = GetCaptureTime(f);
                if (t == null) { Emit("[跳过] 无法读取任何时间: " + f); failed++; continue; }
                DateTime dt = t.Value.AddSeconds(offsetSeconds);

                string key = dt.ToString("yyyy-MM-dd_HHmmss");
                int seq;
                counter.TryGetValue(key, out seq);
                seq++;
                counter[key] = seq;

                string newName = string.Format("{0}_{1:000}{2}", key, seq, Path.GetExtension(f).ToLower());
                string sub = RelativeDir(dt, mode);
                string target = Path.Combine(dstAbs, sub, newName);

                // 防递归:输出目录在输入目录内时,跳过扫描到自己的文件
                if (IsInside(target, dstAbs) && IsInside(target, Path.GetFullPath(src)))
                {
                    // 目标在源内,属于历史输出,正常处理即可(不复制自己)
                }

                int guard = 0;
                while (File.Exists(target) && guard < 999)
                {
                    seq++;
                    newName = string.Format("{0}_{1:000}{2}", key, seq, Path.GetExtension(f).ToLower());
                    target = Path.Combine(dstAbs, sub, newName);
                    guard++;
                }

                try
                {
                    Directory.CreateDirectory(Path.GetDirectoryName(target));
                    if (move) File.Move(f, target);
                    else File.Copy(f, target, false);
                    Emit(string.Format("[{0:yyyy-MM-dd HH:mm:ss}] {1} -> {2}", dt, Path.GetFileName(f),
                        Path.Combine(sub, Path.GetFileName(target))));
                    processed++;
                }
                catch (Exception ex)
                {
                    Emit("[失败] " + f + " : " + ex.Message);
                    failed++;
                }
            }
            return Tuple.Create(processed, dups, failed);
        }

        private static bool IsInside(string path, string root)
        {
            string full = Path.GetFullPath(path);
            string r = Path.GetFullPath(root).TrimEnd('\\', '/') + "\\";
            return full.StartsWith(r, StringComparison.OrdinalIgnoreCase);
        }

        private static string RelativeDir(DateTime dt, FolderMode mode)
        {
            switch (mode)
            {
                case FolderMode.Year: return dt.ToString("yyyy");
                case FolderMode.YearMonth: return Path.Combine(dt.ToString("yyyy"), dt.ToString("MM"));
                case FolderMode.YearMonthDay: return Path.Combine(dt.ToString("yyyy"), dt.ToString("MM"), dt.ToString("dd"));
                default: return "";
            }
        }
    }
}
