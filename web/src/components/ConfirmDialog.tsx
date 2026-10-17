import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

type Props = {
  open: boolean
  title: string
  description: string
  confirm: string
  cancel?: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
  // A third choice, between the two.
  alternative?: string
  onAlternative?: () => void
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirm,
  cancel = 'Cancel',
  destructive,
  onConfirm,
  onCancel,
  alternative,
  onAlternative,
}: Props) {
  return (
    <AlertDialog open={open} onOpenChange={(isOpen) => !isOpen && onCancel()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{cancel}</AlertDialogCancel>
          {alternative && (
            <AlertDialogAction variant="outline" onClick={onAlternative}>
              {alternative}
            </AlertDialogAction>
          )}
          <AlertDialogAction variant={destructive ? 'destructive' : 'default'} onClick={onConfirm}>
            {confirm}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
