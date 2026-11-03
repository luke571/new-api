/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { AuthLayout } from '@/features/auth/auth-layout'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { createServerError } from '@/lib/server-error-message'

function InternalReset() {
  const { t } = useTranslation()
  const [token, setToken] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(event: React.FormEvent) {
    event.preventDefault()
    setBusy(true)
    try {
      const res = await api.post('/api/internal/reset-password', {
        token,
        password,
      })
      if (!res.data.success) throw createServerError(res.data)
      setToken('')
      setPassword('')
      setConfirmation('')
      toast.success(t('Password reset. Please sign in again.'))
      window.location.assign('/sign-in')
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  return (
    <AuthLayout>
      <form onSubmit={submit} className='grid gap-4'>
        <h1 className='text-xl font-semibold'>{t('Password recovery')}</h1>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Ask your administrator for a one-time recovery token. All previous login sessions will be revoked.'
          )}
        </p>
        <Label htmlFor='recovery-token'>{t('Recovery token')}</Label>
        <Input
          id='recovery-token'
          autoComplete='off'
          required
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        <Label htmlFor='recovery-password'>
          {t('New password (15 to 128 characters)')}
        </Label>
        <PasswordInput
          id='recovery-password'
          autoComplete='new-password'
          minLength={15}
          maxLength={128}
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <Label htmlFor='recovery-confirm'>{t('Confirm password')}</Label>
        <PasswordInput
          id='recovery-confirm'
          autoComplete='new-password'
          required
          value={confirmation}
          onChange={(e) => setConfirmation(e.target.value)}
        />
        <Button
          disabled={
            busy ||
            token.length !== 64 ||
            password.length < 15 ||
            password !== confirmation
          }
        >
          {t('Reset password')}
        </Button>
      </form>
    </AuthLayout>
  )
}
export const Route = createFileRoute('/(auth)/internal-reset')({
  component: InternalReset,
})
