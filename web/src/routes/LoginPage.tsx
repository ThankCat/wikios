import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ShieldEllipsis } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { saveAdminToken } from "@/lib/storage";

type Props = {
  mountedWikiName: string;
};

export function LoginPage({ mountedWikiName }: Props) {
  const [token, setToken] = useState("");
  const navigate = useNavigate();

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-lg">
        <CardHeader>
          <CardTitle className="flex items-center gap-3 text-2xl">
            <ShieldEllipsis className="h-7 w-7 text-primary" />
            管理员登录
          </CardTitle>
          <CardDescription>
            输入 Bearer token 以解锁管理员后台。当前连接到 <strong>{mountedWikiName}</strong>。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Input
            placeholder="粘贴 WIKIOS_ADMIN_TOKEN"
            type="password"
            value={token}
            onChange={(event) => setToken(event.target.value)}
          />
          <Button
            className="w-full"
            disabled={!token.trim()}
            onClick={() => {
              saveAdminToken(token.trim());
              navigate("/");
            }}
          >
            进入工作台
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

